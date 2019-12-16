package internal

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

func NewService(queue ProcessingQueue, store ImageStore, ackbus ImageProcessedAckBus, httpTimeout int) *Service {
	svc := Service{
		queue:  queue,
		store:  store,
		ackbus: ackbus,
		httpTimeout: httpTimeout,
	}

	count, err := store.Count()
	if err != nil {
		log.Printf("failed to count initial images: %s\n", err)
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

	s := http.StripPrefix("/docs/", http.FileServer(http.Dir("./../../web/swagger-ui/")))
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

// ServeHTTP implements the http.Handler interface for the Service type.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Service is the struct that contains the server handler as well as
// any references to services that the Service needs.
type Service struct {
	queue       ProcessingQueue
	store       ImageStore
	ackbus      ImageProcessedAckBus
	handler     http.Handler
	httpTimeout int
}

func (svc *Service) imgHandler(rw http.ResponseWriter, req *http.Request) {
	var sizeStr string
	size, ok := req.URL.Query()["size"]
	if !ok || len(size[0]) < 1 {
		sizeStr = ""
	} else {
		sizeStr = size[0]
	}
	img, err := NewImageFromRequest(mux.Vars(req)["filename"], sizeStr)
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

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(svc.httpTimeout)*time.Millisecond)
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

