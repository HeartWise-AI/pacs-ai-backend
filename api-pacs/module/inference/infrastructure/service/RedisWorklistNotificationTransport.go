package service

import (
	"context"
	"errors"

	redis "github.com/go-redis/redis"
)

const DefaultRedisWorklistNotificationChannel = "pacs-ai:inference:worklist:events"

// WorklistNotificationTransport carries internal notification envelopes
// between Go instances. The local broker remains responsible for tenant fan-out.
type WorklistNotificationTransport interface {
	Publish(ctx context.Context, payload []byte) error
	Subscribe(ctx context.Context) (<-chan []byte, error)
}

// RedisWorklistNotificationTransport implements ephemeral multi-instance
// delivery with Redis Pub/Sub. Missed messages are recovered through REST
// snapshots rather than replayed by Redis.
type RedisWorklistNotificationTransport struct {
	Client  *redis.Client
	Channel string
}

func (transport *RedisWorklistNotificationTransport) Publish(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transport.Client == nil {
		return errors.New("redis worklist transport is not configured")
	}
	return transport.Client.Publish(transport.channel(), payload).Err()
}

func (transport *RedisWorklistNotificationTransport) Subscribe(ctx context.Context) (<-chan []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if transport.Client == nil {
		return nil, errors.New("redis worklist transport is not configured")
	}

	subscription := transport.Client.Subscribe(transport.channel())
	if _, err := subscription.Receive(); err != nil {
		_ = subscription.Close()
		return nil, err
	}

	messages := make(chan []byte)
	go func() {
		defer close(messages)
		defer subscription.Close()
		redisMessages := subscription.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case message, open := <-redisMessages:
				if !open {
					return
				}
				payload := []byte(message.Payload)
				select {
				case messages <- payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return messages, nil
}

func (transport *RedisWorklistNotificationTransport) channel() string {
	if transport.Channel != "" {
		return transport.Channel
	}
	return DefaultRedisWorklistNotificationChannel
}
