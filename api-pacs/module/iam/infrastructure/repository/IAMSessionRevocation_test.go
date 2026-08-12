package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	redisTypes "api-pacs/infrastructures/database/redis/types"
	apiError "api-pacs/internal/errors"
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

type sessionRevocationRedis struct {
	redisTypes.RedisDBHandlerInterface
	values      map[string]string
	deleted     []string
	blocked     bool
	blockingKey string
	writtenKey  string
}

func (redis *sessionRevocationRedis) Scan(string) ([]string, error) {
	keys := make([]string, 0, len(redis.values))
	for key := range redis.values {
		keys = append(keys, key)
	}
	return keys, nil
}

func (redis *sessionRevocationRedis) Get(key string) (string, error) {
	value, ok := redis.values[key]
	if !ok {
		return "", errors.New("empty")
	}
	return value, nil
}

func (redis *sessionRevocationRedis) Delete(key string) error {
	redis.deleted = append(redis.deleted, key)
	delete(redis.values, key)
	return nil
}

func (redis *sessionRevocationRedis) SetIfKeyAbsent(blockingKey, key string, _ interface{}, _ time.Duration) (bool, error) {
	redis.blockingKey = blockingKey
	redis.writtenKey = key
	return !redis.blocked, nil
}

func TestSetTokenSessionCannotWriteThroughSuspensionMarker(t *testing.T) {
	redis := &sessionRevocationRedis{blocked: true}
	repository := &IAMCommandRepository{RedisDBHandlerInterface: redis}

	err := repository.SetTokenSession(repositoryTypes.SetTokenSession{
		SessionID: "session-a", TenantID: "tenant-a", UserID: "user-a", Role: "USER", ExpireTimeInSeconds: 900,
	})

	require.EqualError(t, err, apiError.AccountSuspended)
	require.Equal(t, "iam:suspended:tenant-a:user-a", redis.blockingKey)
	require.Equal(t, "session-a", redis.writtenKey)
}

func TestRevokeUserSessionsDeletesOnlyMatchingTenantUser(t *testing.T) {
	redis := &sessionRevocationRedis{values: map[string]string{
		"session-a": `{"tenantId":"tenant-a","userId":"user-a","role":"USER"}`,
		"session-b": `{"tenantId":"tenant-a","userId":"user-b","role":"USER"}`,
		"session-c": `{"tenantId":"tenant-b","userId":"user-a","role":"USER"}`,
		"counter":   "17",
	}}
	repository := &IAMCommandRepository{RedisDBHandlerInterface: redis}

	err := repository.RevokeUserSessions("tenant-a", "user-a")

	require.NoError(t, err)
	require.Equal(t, []string{"session-a"}, redis.deleted)
	require.Contains(t, redis.values, "session-b")
	require.Contains(t, redis.values, "session-c")
}
