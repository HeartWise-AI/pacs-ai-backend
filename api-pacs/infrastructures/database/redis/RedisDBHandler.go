package redis

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	db "github.com/go-redis/redis"
)

var incrementWithExpiryScript = db.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return {count, redis.call("PTTL", KEYS[1])}
`)

// RedisDBHandler handles the methods for the redis database
type RedisDBHandler struct {
	rw     sync.RWMutex
	Client *db.Client
}

// Connect to redis instance
func (r *RedisDBHandler) Connect(address string, password string, dbIndex int) (string, error) {
	opt := &db.Options{
		Addr:     address,
		Password: password, // no password set
		DB:       dbIndex,  // use default DB
	}

	// check for tls based urls
	if len(address) > 30 {
		opt.TLSConfig = &tls.Config{}
	}

	r.Client = db.NewClient(opt)

	// basic test connection in go-redis
	pong, err := r.Client.Ping().Result()
	if err != nil {
		return "", err
	}

	return pong, nil
}

// Delete a value by key
func (r *RedisDBHandler) Delete(key string) error {
	r.rw.Lock()
	defer r.rw.Unlock()

	err := r.Client.Del(key).Err()

	return err
}

// Flush flushes entire selected database
func (r *RedisDBHandler) Flush() {
	r.rw.Lock()
	defer r.rw.Unlock()

	r.Client.FlushDB()
}

// Get the value stored using a specified key (case-sensitive)
func (r *RedisDBHandler) Get(key string) (string, error) {
	r.rw.Lock()
	defer r.rw.Unlock()

	val, err := r.Client.Get(key).Result()
	if err == db.Nil {
		return "", errors.New("empty")
	} else if err != nil {
		return "", err
	}

	return val, nil
}

// IncrementWithExpiry atomically increments a counter and starts its expiry
// window only when the key is first created. It returns the new count and TTL.
func (r *RedisDBHandler) IncrementWithExpiry(key string, expiry time.Duration) (int64, time.Duration, error) {
	r.rw.Lock()
	defer r.rw.Unlock()

	result, err := incrementWithExpiryScript.Run(r.Client, []string{key}, expiry.Milliseconds()).Result()
	if err != nil {
		return 0, 0, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return 0, 0, fmt.Errorf("unexpected increment result: %T", result)
	}

	count, ok := values[0].(int64)
	if !ok {
		return 0, 0, fmt.Errorf("unexpected increment count: %T", values[0])
	}
	ttlMilliseconds, ok := values[1].(int64)
	if !ok {
		return 0, 0, fmt.Errorf("unexpected increment ttl: %T", values[1])
	}

	return count, time.Duration(ttlMilliseconds) * time.Millisecond, nil
}

// Set a key-value pair to redis database (case-sensitive)
func (r *RedisDBHandler) Set(key string, val interface{}, exp time.Duration) error {
	r.rw.Lock()
	defer r.rw.Unlock()

	err := r.Client.Set(key, val, exp).Err()

	return err
}
