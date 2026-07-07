package redis

import (
	"testing"
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
