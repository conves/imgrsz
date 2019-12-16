package main

import (
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/daddye/vips"
	"github.com/go-redis/redis"

	"github.com/conves/imgrsz/internal"
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

func main() {
	// Overwrite redis-url flag with env var if provided
	envRedisDsn := os.Getenv("IMGRESIZER_REDIS_URL")
	if envRedisDsn != "" {
		*redisDsn = envRedisDsn
	}

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

	ackbus := internal.NewRedisImageProcessedAckBus(client, *redisDoneCh)
	defer ackbus.Close()

	queue := internal.NewRedisQueue(client)
	store := internal.NewRedisCachedFsImageStore(client, *basepath)

	// Start image processing workers
	for i := 0; i < *workers; i++ {
		go ProcessingWorker{queue: queue, store: store, ackbus: ackbus, basepath: *basepath}.Do()
	}

	fileWatchingWorker := internal.NewFileWatchingWorker(queue, store, ackbus, *basepath)
	go fileWatchingWorker.Do()

	svc := internal.NewService(queue, store, ackbus, *timeout)
	log.Printf("starting http server on: %s", *addr)
	log.Fatalf("http server crashed: %s", http.ListenAndServe(*addr, svc))
}

// ProcessingWorker process images to be resized
type ProcessingWorker struct {
	queue    internal.ProcessingQueue
	store    internal.ImageStore
	ackbus   internal.ImageProcessedAckBus
	basepath string
}

func (w ProcessingWorker) Do() {
	for {
		func() {
			var err error
			var img internal.Imgmeta

			img, err = w.queue.Dequeue()
			if err == internal.ErrNil {
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
