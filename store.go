package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/go-redis/redis"
)

type ImageStore interface {
	Has(img imgmeta) (bool, error)
	LoadNew() ([]imgmeta, error) // todo decide whether we need it or not
	Save(img imgmeta, content []byte) error
	Serve(rw http.ResponseWriter, img imgmeta) error
}

type RedisCachedLocalImageStore struct {
	client    *redis.Client
	basepath  string
	lastCheck time.Time
}

func (r RedisCachedLocalImageStore) LoadNew() ([]imgmeta, error) {
	var lastModified time.Time
	lm, err := r.client.Get("last-modified").Result()
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to read last modified from Redis: %s", err))
	}

	lastModified, err = time.Parse(time.RFC3339, lm)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to parse last modified: %s", err))
	}

	files, err := ioutil.ReadDir(r.basepath)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to read files from disk: %s", err))
	}

	var newimgs []imgmeta
	for _, f := range files {
		if f.ModTime().After(lastModified) {
			newimgs = append(newimgs, imgmeta{Original: f.Name()})
		}
	}

	var imgs []imgmeta
	for _, img := range newimgs {
		// Build image meta
		img.Width, img.Height, err = readImageSize(img)
		if err != nil {
			log.Printf("failed to read image size: %s\n", err)
			continue
		}

		imgs = append(imgs, img)
	}

	return imgs, r.client.Set("last-modified", lastModified.Format(time.RFC3339), 0).Err()
}

var ErrOriginalNotFound = errors.New("original image not found")

func (r RedisCachedLocalImageStore) Has(img imgmeta) (bool, error) {
	info, err := os.Stat(path.Join(r.basepath, img.Name()))
	if os.IsNotExist(err) {
		_, err := os.Stat(path.Join(r.basepath, img.Original))
		if os.IsNotExist(err) {
			return false, ErrOriginalNotFound
		}
		return false, nil
	}
	return !info.IsDir(), nil
}

// Save writes resized image
func (r RedisCachedLocalImageStore) Save(img imgmeta, content []byte) error {
	if content != nil {
		return ioutil.WriteFile(path.Join(r.basepath, img.Name()), content, 0644)
	}
	return nil
}

func (r RedisCachedLocalImageStore) Serve(w http.ResponseWriter, img imgmeta) error {
	imgFile, err := os.Open(path.Join(r.basepath, img.Name()))
	if err != nil {
		return errors.New("failed to open file: " + err.Error())
	}
	defer imgFile.Close()

	fileInfo, err := imgFile.Stat()
	if err != nil {
		return errors.New("failed to read file stats: " + err.Error())
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(int(fileInfo.Size())))
	w.Header().Set("Last-Modified", fileInfo.ModTime().Format(time.RFC1123))

	_, err = io.Copy(w, imgFile)
	return err
}

func newRedisCachedFsImageStore(client *redis.Client, basepath string) ImageStore {
	return RedisCachedLocalImageStore{
		client:   client,
		basepath: basepath,
	}
}
