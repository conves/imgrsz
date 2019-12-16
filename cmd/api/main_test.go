package main

import (
	"flag"
	"fmt"
	"github.com/conves/imgrsz/internal"
	"image/jpeg"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-redis/redis"
)

// TestMain calls testMain and passes the returned exit code to os.Exit(). The reason
// that TestMain is basically a wrapper around testMain is because os.Exit() does not
// respect deferred functions, so this configuration allows for a deferred function.
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(testMain(m))
}

var svc *internal.Service

// testMain returns an integer denoting an exit code to be returned and used in
// TestMain. The exit code 0 denotes success, all other codes denote failure.
func testMain(m *testing.M) int {
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

	svc = internal.NewService(queue, store, ackbus, *timeout)

	return m.Run()
}

func Test_getImage(t *testing.T) {
	// Not found image
	{
		req, err := http.NewRequest(http.MethodGet, "/image/12345678.jpg", nil)
		if err != nil {
			t.Errorf("error creating request: %v", err)
		}

		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)

		if e, a := http.StatusNotFound, w.Code; e != a {
			t.Errorf("expected status code: %v, got status code: %v", e, a)
		}
	}

	// Bad request
	{
		req, err := http.NewRequest(http.MethodGet, "/image/12345678.jpg?size=123xdf123a", nil)
		if err != nil {
			t.Errorf("error creating request: %v", err)
		}

		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)

		if e, a := http.StatusBadRequest, w.Code; e != a {
			t.Errorf("expected status code: %v, got status code: %v", e, a)
		}
	}

	// Found original image
	{
		req, err := http.NewRequest(http.MethodGet, "/image/beautiful_landscape_1.jpg", nil)
		if err != nil {
			t.Errorf("error creating request: %v", err)
		}

		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)

		if e, a := http.StatusOK, w.Code; e != a {
			t.Errorf("expected status code: %v, got status code: %v", e, a)
		}
	}

	var lastModified string

	// Resized image
	{
		width := 150
		height := 150
		req, err := http.NewRequest(
			http.MethodGet,
			fmt.Sprintf("/image/beautiful_landscape_1.jpg?size=%dx%d", width, height),
			nil)
		if err != nil {
			t.Errorf("error creating request: %v", err)
		}

		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)

		if e, a := http.StatusOK, w.Code; e != a {
			t.Errorf("expected status code: %v, got status code: %v", e, a)
		}

		img, err := jpeg.DecodeConfig(w.Body)
		if err != nil {
			t.Errorf("failed to decode received image: %s", err)
		}
		if width != img.Width {
			t.Errorf("expected width: %v, got widht: %v", width, img.Width)
		}

		//if height != img.Height {
		//	t.Errorf("expected height: %v, got height: %v", height, img.Height)
		//}

		lastModified = w.Header().Get("Last-Modified")
		if lastModified == "" {
			t.Error("expected non empty last-modified header")
		}
	}

	// Cached image
	{
		width := 150
		height := 150
		req, err := http.NewRequest(
			http.MethodGet,
			fmt.Sprintf("/image/beautiful_landscape_1.jpg?size=%dx%d", width, height),
			nil)
		if err != nil {
			t.Errorf("error creating request: %v", err)
		}

		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)

		if e, a := http.StatusOK, w.Code; e != a {
			t.Errorf("expected status code: %v, got status code: %v", e, a)
		}

		currentLastModified := w.Header().Get("Last-Modified")
		if lastModified != currentLastModified {
			t.Errorf("expected last-modified: %v, got last-modified: %v", lastModified, currentLastModified)
		}
	}
}
