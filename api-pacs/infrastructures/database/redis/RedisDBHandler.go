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

var setIfKeyAbsentScript = db.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then
  return 0
end
redis.call("SET", KEYS[2], ARGV[1], "PX", ARGV[2])
return 1
`)

var deleteIfValueMatchesScript = db.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
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
	r.rw.RLock()
	defer r.rw.RUnlock()

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

// Scan returns all keys matching pattern without blocking Redis with KEYS.
func (r *RedisDBHandler) Scan(pattern string) ([]string, error) {
	var keys []string
	var cursor uint64
	for {
		r.rw.RLock()
		batch, nextCursor, err := r.Client.Scan(cursor, pattern, 100).Result()
		r.rw.RUnlock()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			return keys, nil
		}
	}
}

// Set a key-value pair to redis database (case-sensitive)
func (r *RedisDBHandler) Set(key string, val interface{}, exp time.Duration) error {
	r.rw.Lock()
	defer r.rw.Unlock()

	err := r.Client.Set(key, val, exp).Err()

	return err
}

// SetIfAbsent writes a key only when it does not already exist.
func (r *RedisDBHandler) SetIfAbsent(key string, val interface{}, exp time.Duration) (bool, error) {
	r.rw.Lock()
	defer r.rw.Unlock()

	return r.Client.SetNX(key, val, exp).Result()
}

// DeleteIfValueMatches deletes a key only when it is still owned by val.
func (r *RedisDBHandler) DeleteIfValueMatches(key string, val interface{}) (bool, error) {
	r.rw.Lock()
	defer r.rw.Unlock()

	result, err := deleteIfValueMatchesScript.Run(r.Client, []string{key}, val).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}

// SetIfKeyAbsent atomically writes key only when blockingKey does not exist.
// It prevents a concurrent request from recreating a session after suspension.
func (r *RedisDBHandler) SetIfKeyAbsent(blockingKey, key string, val interface{}, exp time.Duration) (bool, error) {
	r.rw.Lock()
	defer r.rw.Unlock()

	result, err := setIfKeyAbsentScript.Run(
		r.Client,
		[]string{blockingKey, key},
		val,
		exp.Milliseconds(),
	).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}
