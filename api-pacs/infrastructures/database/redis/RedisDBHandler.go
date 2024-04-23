package redis

import (
	"crypto/tls"
	"errors"
	"sync"
	"time"

	db "github.com/go-redis/redis"
)

// RedisDBHandler handles the methods for the redis database
type RedisDBHandler struct {
	rw     sync.RWMutex
	Client *db.Client
}

// Connect to redis instance
func (r *RedisDBHandler) Connect(address string, password string, dbIndex int) (string, error) {
	r.rw.Lock()
	defer r.rw.Unlock()

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
		return "", errors.New("Empty")
	} else if err != nil {
		return "", err
	}

	return val, nil
}

// Set a key-value pair to redis database (case-sensitive)
func (r *RedisDBHandler) Set(key string, val interface{}, exp time.Duration) error {
	r.rw.Lock()
	defer r.rw.Unlock()

	err := r.Client.Set(key, val, exp).Err()

	return err
}
