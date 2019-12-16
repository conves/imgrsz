package main

import (
	"context"
	"errors"
	"time"

	"github.com/ReneKroon/ttlcache"
	"github.com/go-redis/redis"
)

type ImageProcessedAckBus interface {
	Send(key string) error
	Receive(ctx context.Context, key string) error
	Close()
}

type RedisImageProcessedAckBus struct {
	client     *redis.Client
	pubsub     *redis.PubSub
	pubsubchan string
	ackcache   *ttlcache.Cache // Map of "image processed" acknowledgement channels; multiplexed from pubsubchan
	acksttl    time.Duration
}

func (r RedisImageProcessedAckBus) Close() {
	r.pubsub.Close()
	r.ackcache.Close()
}

func (r RedisImageProcessedAckBus) Send(key string) error {
	err := r.client.Publish(r.pubsubchan, key).Err()
	if _, ok := r.ackcache.Get(key); !ok {
		r.ackcache.SetWithTTL(key, make(chan struct{}, 1), r.acksttl)
	}
	return err
}

func (r RedisImageProcessedAckBus) Receive(ctx context.Context, key string) error {
	// Because receive() can occur before send()
	if _, ok := r.ackcache.Get(key); !ok {
		r.ackcache.SetWithTTL(key, make(chan struct{}, 1), r.acksttl)
	}

	ch, _ := r.ackcache.Get(key)
	recvch := ch.(chan struct{})

	select {
	case <-ctx.Done():
		return errors.New("context deadline")
	case <-recvch:
	}
	return nil
}

func newRedisImageProcessedAckBus(client *redis.Client, channel string) ImageProcessedAckBus {
	bus := RedisImageProcessedAckBus{client: client, pubsubchan: channel}
	bus.pubsub = client.PSubscribe(*redisDoneCh)
	bus.ackcache = ttlcache.NewCache()

	recvch := bus.pubsub.Channel()
	go func() {
		for {
			select {
			case m := <-recvch:
				if ch, ok := bus.ackcache.Get(m.Payload); ok {
					ackch := ch.(chan struct{})
					ackch <- struct{}{}
				}
			}
		}
	}()

	return bus
}
