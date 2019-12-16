package main

import (
	"flag"
	"log"
	"net/http"
	"os"

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

	fileWatchingWorker := internal.NewFileWatchingWorker(queue, store, ackbus, *basepath)
	go fileWatchingWorker.Do()

	svc := internal.NewService(queue, store, ackbus, *timeout)
	log.Printf("starting http server on: %s", *addr)
	log.Fatalf("http server crashed: %s", http.ListenAndServe(*addr, svc))
}
