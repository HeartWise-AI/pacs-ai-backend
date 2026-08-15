package types

import "time"

// RedisDBHandlerInterface holds the list of implementable methods for the RedisDBHandler
type RedisDBHandlerInterface interface {
	Delete(key string) error
	DeleteIfValueMatches(key string, val interface{}) (bool, error)
	Flush()
	Get(key string) (string, error)
	GetCounterWithExpiry(key string) (int64, time.Duration, error)
	IncrementWithExpiry(key string, expiry time.Duration) (int64, time.Duration, error)
	Scan(pattern string) ([]string, error)
	Set(key string, val interface{}, exp time.Duration) error
	SetIfAbsent(key string, val interface{}, exp time.Duration) (bool, error)
	SetIfKeyAbsent(blockingKey, key string, val interface{}, exp time.Duration) (bool, error)
}
