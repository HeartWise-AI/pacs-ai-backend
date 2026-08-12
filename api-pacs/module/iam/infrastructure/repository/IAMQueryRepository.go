package repository

import (
	"encoding/json"
	"errors"
	"log"

	"api-pacs/infrastructures/database/redis/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/domain/entity"
)

// IAMQueryRepository handles iam query repository
type IAMQueryRepository struct {
	types.RedisDBHandlerInterface
}

// IsUserSuspended checks the revocation marker used by normal and proxy auth guards.
func (repository *IAMQueryRepository) IsUserSuspended(tenantID, userID string) (bool, error) {
	_, err := repository.RedisDBHandlerInterface.Get(userSuspensionKey(tenantID, userID))
	if err == nil {
		return true, nil
	}
	if err.Error() == "empty" {
		return false, nil
	}
	log.Println(err)
	return false, errors.New(apiError.DatabaseError)
}

// GetTokenSession get token session by session id
func (repository *IAMQueryRepository) GetTokenSession(key string) (entity.TokenSession, error) {
	var session entity.TokenSession

	data, err := repository.RedisDBHandlerInterface.Get(key)
	if err != nil {
		if err.Error() == "empty" {
			return entity.TokenSession{}, errors.New(apiError.MissingRecord)
		}

		log.Println(err)
		return entity.TokenSession{}, errors.New(apiError.DatabaseError)
	}

	// decode to struct
	if err := json.Unmarshal([]byte(data), &session); err != nil {
		log.Println(err)
		return entity.TokenSession{}, errors.New(apiError.DatabaseError)
	}

	return session, nil
}
