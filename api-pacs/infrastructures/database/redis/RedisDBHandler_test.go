package redis

import (
	"os"
	"testing"
	"time"
)

func TestConnection(t *testing.T) {
	db := new(RedisDBHandler)

	res, err := db.Connect("localhost:6379", "", 0)
	if err != nil {
		t.Error(err)
		return
	}

	t.Log(res)
}

func TestWriteRead(t *testing.T) {
	t.Run("write some data", func(t *testing.T) {
		// connect to redis
		db := new(RedisDBHandler)

		_, err := db.Connect("localhost:6379", "", 0)
		if err != nil {
			t.Error(err)
			return
		}

		// write
		err = db.Set("title", "hi", 0)
		if err != nil {
			t.Error(err)
			return
		}

		t.Log("success")
	})

	t.Run("read some data", func(t *testing.T) {
		// connect to redis
		db := new(RedisDBHandler)

		_, err := db.Connect("localhost:6379", "", 0)
		if err != nil {
			t.Error(err)
			return
		}

		// write the key this subtest reads so it can run in isolation
		err = db.Set("title", "hi", 0)
		if err != nil {
			t.Error(err)
			return
		}

		// read
		data, err := db.Get("title")
		if err != nil {
			t.Error(err)
			return
		}

		if data != "hi" {
			t.Errorf("expected %q, got %q", "hi", data)
		}
	})
}

func TestDeleteByKey(t *testing.T) {
	// connect to redis
	db := new(RedisDBHandler)

	_, err := db.Connect("localhost:6379", "", 0)
	if err != nil {
		t.Error(err)
		return
	}

	err = db.Delete("title")
	if err != nil {
		t.Error(err)
		return
	}

	t.Log("successfully deleted")
}

func TestFlushDB(t *testing.T) {
	// connect to redis
	db := new(RedisDBHandler)

	_, err := db.Connect("localhost:6379", "", 0)
	if err != nil {
		t.Error(err)
		return
	}

	db.Flush()
}

func TestIncrementWithExpiry(t *testing.T) {
	db := new(RedisDBHandler)
	_, err := db.Connect("localhost:6379", os.Getenv("REDIS_PASSWORD"), 0)
	if err != nil {
		t.Fatal(err)
	}
	const key = "test:increment-with-expiry"
	requireNoError(t, db.Delete(key))
	t.Cleanup(func() { _ = db.Delete(key) })

	firstCount, firstTTL, err := db.IncrementWithExpiry(key, time.Minute)
	requireNoError(t, err)
	if firstCount != 1 {
		t.Fatalf("expected first count 1, got %d", firstCount)
	}
	if firstTTL <= 0 || firstTTL > time.Minute {
		t.Fatalf("expected a positive TTL no greater than one minute, got %s", firstTTL)
	}

	secondCount, secondTTL, err := db.IncrementWithExpiry(key, time.Minute)
	requireNoError(t, err)
	if secondCount != 2 {
		t.Fatalf("expected second count 2, got %d", secondCount)
	}
	if secondTTL <= 0 || secondTTL > firstTTL {
		t.Fatalf("expected the original expiry window to be preserved, got first=%s second=%s", firstTTL, secondTTL)
	}
}

func TestGetCounterWithExpiry(t *testing.T) {
	db := new(RedisDBHandler)
	_, err := db.Connect("localhost:6379", os.Getenv("REDIS_PASSWORD"), 0)
	if err != nil {
		t.Fatal(err)
	}
	const key = "test:get-counter-with-expiry"
	requireNoError(t, db.Delete(key))
	t.Cleanup(func() { _ = db.Delete(key) })

	count, ttl, err := db.GetCounterWithExpiry(key)
	requireNoError(t, err)
	if count != 0 || ttl != 0 {
		t.Fatalf("expected a missing counter to return zero values, got count=%d ttl=%s", count, ttl)
	}

	_, _, err = db.IncrementWithExpiry(key, time.Minute)
	requireNoError(t, err)
	count, ttl, err = db.GetCounterWithExpiry(key)
	requireNoError(t, err)
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("expected a positive TTL no greater than one minute, got %s", ttl)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
