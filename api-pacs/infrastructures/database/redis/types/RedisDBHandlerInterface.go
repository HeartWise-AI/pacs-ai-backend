package types

import "time"

// RedisDBHandlerInterface holds the list of implementable methods for the RedisDBHandler
type RedisDBHandlerInterface interface {
	Delete(key string) error
	Flush()
	Get(key string) (string, error)
	IncrementWithExpiry(key string, expiry time.Duration) (int64, time.Duration, error)
	Set(key string, val interface{}, exp time.Duration) error
}
