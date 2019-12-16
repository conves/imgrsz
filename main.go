package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/daddye/vips"
	"github.com/go-redis/redis"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// App config flags
var (
	addr        = flag.String("addr", ":8080", "http server address")
	redisDsn    = flag.String("redis-url", "localhost:6379", "redis dsn")
	redisPass   = flag.String("redis-pass", "", "redis password")
	redisDb     = flag.Int("redis-db", 0, "redis database")
	redisDoneCh = flag.String("redis-done-chan", "processed", "redis image done processing channel")
	workers     = flag.Int("workers", 3, "number of workers")
	basepath    = flag.String("basepath", "images", "path for local images")
	timeout     = flag.Int("timeout", 2000, "timeout for image processing")
)

// Prometheus metrics constructors
var (
	initialImages = promauto.NewCounter(prometheus.CounterOpts{
		Name: "imgresizer_initial_images",
		Help: "The total number of initial images",
	})

	resizedImages = promauto.NewCounter(prometheus.CounterOpts{
		Name: "imgresizer_resized_images",
		Help: "The total number of resized images",
	})

	currentImages = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "imgresizer_current_images",
		Help: "The number of current images",
	})

	cacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "imgresizer_cache_hits",
		Help: "The total number of cache hits",
	})

	cacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "imgresizer_cache_missed",
		Help: "The total number of cache misses",
	})

	responseTimesStatuses = promauto.NewSummaryVec(prometheus.SummaryOpts{
		Name: "imgresizer_response_times_by_statuses",
		Help: "Response times by statuses",
	}, []string{"status"})
)

func main() {
	client := redis.NewClient(&redis.Options{
		Addr:     *redisDsn,
		Password: *redisPass,
		DB:       *redisDb,
	})

	_, err := client.Ping().Result()
	if err != nil {
		log.Fatalf("failed to connect to Redis: %s", err)
	}
	defer client.Close()

	// client.FlushDB()

	ackbus := newRedisImageProcessedAckBus(client, *redisDoneCh)
	defer ackbus.Close()

	queue := newRedisQueue(client)
	store := newRedisCachedFsImageStore(client, *basepath)

	// Start image processing workers
	for i := 0; i < *workers; i++ {
		go processingWorker{queue: queue, store: store, ackbus: ackbus, basepath: *basepath}.Do()
	}

	go fileWatchingWorker{queue: queue, basepath: *basepath}.Do()

	svc := newService(queue, store, ackbus)
	log.Printf("starting http server on: %s", *addr)
	log.Fatalf("http server crashed: %s", http.ListenAndServe(*addr, svc))
}

func newService(queue ProcessingQueue, store ImageStore, ackbus ImageProcessedAckBus) *service {
	svc := service{
		queue:  queue,
		store:  store,
		ackbus: ackbus,
	}

	count, err := countImages()
	if err != nil {
		log.Printf("failed to count initial images in %s: %s\n", *basepath, err)
	}
	initialImages.Add(float64(count))

	r := mux.NewRouter()

	// swagger:operation GET /image/{filename} Images ServeAndResize
	// ---
	// parameters:
	// - name: filename
	//   in: path
	//   required: true
	//   type: string
	//   format: uuid
	// - name: size
	//   in: query
	//   required: false
	//   type: string
	//   pattern: '^[0-9]+x[0-9]+$'
	// responses:
	//   200:
	r.Handle("/image/{filename}", metricsMdw(http.HandlerFunc(svc.imgHandler)))

	r.Handle("/metrics", promhttp.Handler())

	s := http.StripPrefix("/docs/", http.FileServer(http.Dir("./swagger-ui/")))
	r.PathPrefix("/docs/").Handler(s).Methods(http.MethodGet)

	svc.handler = r

	return &svc
}

type statusWriter struct {
	http.ResponseWriter
	status int
	length int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.length += n
	return n, err
}

func metricsMdw(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := statusWriter{ResponseWriter: w}
		start := time.Now()
		h.ServeHTTP(&sw, r)
		duration := time.Now().Sub(start).Seconds()
		responseTimesStatuses.WithLabelValues(strconv.Itoa(sw.status)).Observe(duration)
	})
}

// ServeHTTP implements the http.Handler interface for the service type.
func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// service is the struct that contains the server handler as well as
// any references to services that the service needs.
type service struct {
	queue   ProcessingQueue
	store   ImageStore
	ackbus  ImageProcessedAckBus
	handler http.Handler
}

func (svc *service) imgHandler(rw http.ResponseWriter, req *http.Request) {
	var sizeStr string
	size, ok := req.URL.Query()["size"]
	if !ok || len(size[0]) < 1 {
		sizeStr = ""
	} else {
		sizeStr = size[0]
	}
	img, err := newImageFromRequest(mux.Vars(req)["filename"], sizeStr)
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)
		rw.Write([]byte("size must be formatted as 123x123"))
		return
	}

	// Check image existence and handle failure
	isCached, err := svc.store.Has(img)
	if err == ErrOriginalNotFound {
		rw.WriteHeader(http.StatusNotFound)
		rw.Write([]byte("not found"))
		return
	}
	if err != nil {
		log.Printf("failed to check imgmeta existence: %s\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !isCached {
		cacheMisses.Add(1)
		// Enqueue an image resizing task
		if err := svc.queue.Enqueue(img); err != nil {
			log.Printf("failed to enqueue an imgmeta for processing: %s\n", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Millisecond)
		defer cancel()

		err = svc.ackbus.Receive(ctx, img.Name())
		if err != nil {
			log.Printf("failed to receive a processed image: %s\n", err)
			rw.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		resizedImages.Add(1)
	} else {
		cacheHits.Add(1)
	}

	if err := svc.store.Serve(rw, img); err != nil {
		log.Printf("failed to serve an imgmeta: %s\n", err)
		rw.WriteHeader(http.StatusInternalServerError)
	}
}

var (
	resRegexp            = regexp.MustCompile("[0-9]+x[0-9]+$")
	ErrInvalidResolution = errors.New("invalid resolution")
)

func newImageFromRequest(filename string, resolution string) (img imgmeta, err error) {
	img.Original = filename

	if resolution == "" {
		img.IsOriginal = true
		return img, nil
	}

	// Handle invalid size
	if !resRegexp.MatchString(resolution) {
		return img, ErrInvalidResolution
	}
	size := strings.Split(resolution, "x")

	img.Width, err = strconv.Atoi(size[0])
	if err != nil {
		return img, ErrInvalidResolution
	}

	img.Height, err = strconv.Atoi(size[1])
	if err != nil {
		return img, ErrInvalidResolution
	}

	return
}

// fileWatchingWorker watches for new images and push them into the processing queue
type fileWatchingWorker struct {
	queue    ProcessingQueue
	store    ImageStore
	pubsub   ImageProcessedAckBus
	basepath string
}

func (w fileWatchingWorker) Do() {
	for {
		count, err := countImages()
		if err != nil {
			log.Printf("failed to count images in %s: %s\n", *basepath, err)
			continue
		}

		currentImages.Set(float64(count))

		// todo decide whether we need to do something with the new images or not
		//imgs, err := w.store.LoadNew()
		//if err != nil {
		//	log.Printf("failed to read new images for processing: %s\n", err)
		//	time.Sleep(time.Millisecond * 100)
		//	continue
		//}
		//
		//for _, img := range imgs {
		//	if err := w.store.Save(img, nil); err != nil {
		//		log.Printf("failed to enqueue a new image for processing: %s\n", err)
		//	}
		//}

		time.Sleep(time.Millisecond * 100)
	}
}

func countImages() (int, error) {
	var i int
	files, err := ioutil.ReadDir(*basepath)
	if err != nil {
		return 0, err
	}
	for _, file := range files {
		if !file.IsDir() {
			i++
		}
	}
	return i, nil
}

// processingWorker process images to be resized
type processingWorker struct {
	queue    ProcessingQueue
	store    ImageStore
	ackbus   ImageProcessedAckBus
	basepath string
}

func (w processingWorker) Do() {
	for {
		func() {
			var err error
			var img imgmeta

			img, err = w.queue.Dequeue()
			if err == ErrNil {
				time.Sleep(10 * time.Millisecond)
				return
			}
			if err != nil {
				log.Printf("failed to deque a task: %s\n", err)
				return
			}

			defer func() {
				if err != nil {
					if err = w.queue.PriorityEnqueue(img); err != nil {
						log.Printf("failed to re-enqueue an image for processing: %s\n", err)
					}
				}
			}()

			// Resize image
			var file *os.File

			file, err = os.Open(path.Join(w.basepath, img.Original))
			if err != nil {
				log.Printf("failed to open image for resizing: %s\n", err)
				return
			}
			inBuf, err := ioutil.ReadAll(file)
			if err != nil {
				log.Printf("failed to read image content: %s\n", err)
				return
			}
			options := vips.Options{
				Width:   img.Width,
				Height:  img.Height,
				Quality: 100,
				Format:  vips.JPEG,
			}
			buf, err := vips.Resize(inBuf, options)
			if err != nil {
				log.Printf("failed to resize image: %s\n", err)
				return
			}

			// Save image
			if err = w.store.Save(img, buf); err != nil {
				log.Printf("error saving an image: %s\n", err)
			}

			if err = w.ackbus.Send(img.Name()); err != nil {
				log.Printf("error saving an ack msg: %s\n", err)
			}
		}()
	}
}

type imgmeta struct {
	Original   string `json:"original"`
	IsOriginal bool   `json:"is_original"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// Name generates an image name; for Original images, name remains the same;
// for new images, name is formatted as {originalFilename_1200x700.extension}
func (img imgmeta) Name() string {
	if img.IsOriginal {
		return img.Original
	}

	ext := filepath.Ext(img.Original)
	filenameWithoutExt := strings.TrimSuffix(img.Original, ext)
	return fmt.Sprintf("%s_%dx%d%s", filenameWithoutExt, img.Width, img.Height, ext)
}

func readImageSize(img imgmeta) (width, height int, err error) {
	if reader, err := os.Open(filepath.Join(*basepath, img.Original)); err == nil {
		defer reader.Close()
		im, _, err := image.DecodeConfig(reader)
		if err != nil {
			return 0, 0, errors.New("error reading file info")
		}
		return im.Width, im.Height, nil
	}
	return 0, 0, errors.New("error opening file info")
}
