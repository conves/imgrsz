package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-redis/redis"
)

type ProcessingQueue interface {
	Enqueue(img imgmeta) error
	PriorityEnqueue(img imgmeta) error
	Dequeue() (img imgmeta, err error)
}

type RedisProcessingQueue struct {
	client *redis.Client
}

func (r RedisProcessingQueue) PriorityEnqueue(img imgmeta) error {
	enc, err := json.Marshal(img)
	if err != nil {
		return errors.New(fmt.Sprintf("failed to encode image meta to json: %s", err))
	}
	return r.client.RPush("queue:images", enc).Err()
}

func (r RedisProcessingQueue) Enqueue(img imgmeta) error {
	serialized, err := json.Marshal(img)
	if err != nil {
		return errors.New(fmt.Sprintf("failed to encode image meta to json: %s", err))
	}
	return r.client.LPush("queue:images", string(serialized)).Err()
}

var ErrNil error = errors.New("nil result")

func (r RedisProcessingQueue) Dequeue() (img imgmeta, err error) {
	data, err := r.client.LPop("queue:images").Result()
	if err == redis.Nil {
		return img, ErrNil
	}
	if err != nil {
		return img, errors.New(fmt.Sprintf("failed to get image meta from Redis: %s", err))
	}
	if err = json.Unmarshal([]byte(data), &img); err != nil {
		return img, errors.New(fmt.Sprintf("failed to encode image meta to json: %s", err))
	}
	return
}

func newRedisQueue(client *redis.Client) ProcessingQueue {
	return RedisProcessingQueue{client: client}
}
