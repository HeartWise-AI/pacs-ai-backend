package repository

import (
	"encoding/json"
	"errors"
	"log"
	"time"

	"api-pacs/infrastructures/database/redis/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/domain/entity"
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

// IAMCommandRepository handles iam command repository
type IAMCommandRepository struct {
	types.RedisDBHandlerInterface
}

// DeleteTokenSession delete token session
func (repository *IAMCommandRepository) DeleteTokenSession(key string) error {
	err := repository.RedisDBHandlerInterface.Delete(key)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// IsEmailVerificationCooldownActive checks whether a verification email was sent recently.
func (repository *IAMCommandRepository) IsEmailVerificationCooldownActive(key string) (bool, error) {
	_, err := repository.RedisDBHandlerInterface.Get(key)
	if err == nil {
		return true, nil
	}
	if err.Error() == "empty" {
		return false, nil
	}

	log.Println(err)
	return false, errors.New(apiError.DatabaseError)
}

// SetEmailVerificationCooldown rate-limits verification email sends for one minute.
func (repository *IAMCommandRepository) SetEmailVerificationCooldown(key string) error {
	err := repository.RedisDBHandlerInterface.Set(key, "1", time.Minute)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// SetAuthSession places a new session (or update) to the redis instance
func (repository *IAMCommandRepository) SetTokenSession(data repositoryTypes.SetTokenSession) error {
	session := entity.TokenSession{
		TenantID: data.TenantID,
		UserID:   data.UserID,
		Role:     data.Role,
	}

	// convert to string
	encodedStr, _ := json.Marshal(session)

	err := repository.RedisDBHandlerInterface.Set(data.SessionID, encodedStr, time.Duration(data.ExpireTimeInSeconds)*time.Second)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}
