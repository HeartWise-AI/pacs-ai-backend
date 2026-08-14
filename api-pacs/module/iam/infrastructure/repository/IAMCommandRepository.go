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

func userSuspensionKey(tenantID, userID string) string {
	return "iam:suspended:" + tenantID + ":" + userID
}

func userAccessTransitionKey(tenantID, userID string) string {
	return "iam:access-transition:" + tenantID + ":" + userID
}

// AcquireUserAccessTransition serializes account-state decisions across API replicas.
func (repository *IAMCommandRepository) AcquireUserAccessTransition(tenantID, userID, ownerToken string, ttl time.Duration) (bool, error) {
	acquired, err := repository.RedisDBHandlerInterface.SetIfAbsent(
		userAccessTransitionKey(tenantID, userID), ownerToken, ttl,
	)
	if err != nil {
		log.Println(err)
		return false, errors.New(apiError.DatabaseError)
	}
	return acquired, nil
}

// ReleaseUserAccessTransition removes only the lock owned by this operation.
func (repository *IAMCommandRepository) ReleaseUserAccessTransition(tenantID, userID, ownerToken string) error {
	_, err := repository.RedisDBHandlerInterface.DeleteIfValueMatches(
		userAccessTransitionKey(tenantID, userID), ownerToken,
	)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}
	return nil
}

// SetUserSuspended installs the fail-closed marker checked by every session write.
// It reports whether this call created the marker so callers never remove one
// that predated their operation during rollback.
func (repository *IAMCommandRepository) SetUserSuspended(tenantID, userID string) (bool, error) {
	created, err := repository.RedisDBHandlerInterface.SetIfAbsent(userSuspensionKey(tenantID, userID), "1", 0)
	if err != nil {
		log.Println(err)
		return false, errors.New(apiError.DatabaseError)
	}
	return created, nil
}

// ClearUserSuspension allows sessions to be created again after reactivation.
func (repository *IAMCommandRepository) ClearUserSuspension(tenantID, userID string) error {
	if err := repository.RedisDBHandlerInterface.Delete(userSuspensionKey(tenantID, userID)); err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}
	return nil
}

// RevokeUserSessions removes both current and pre-feature sessions for one user.
func (repository *IAMCommandRepository) RevokeUserSessions(tenantID, userID string) error {
	keys, err := repository.RedisDBHandlerInterface.Scan("*")
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	for _, key := range keys {
		encoded, getErr := repository.RedisDBHandlerInterface.Get(key)
		if getErr != nil {
			continue
		}
		var session entity.TokenSession
		if json.Unmarshal([]byte(encoded), &session) != nil {
			continue
		}
		if session.Role != entity.OwnerRole && session.Role != entity.AdminRole && session.Role != entity.UserRole {
			continue
		}
		if session.TenantID == tenantID && session.UserID == userID {
			if deleteErr := repository.RedisDBHandlerInterface.Delete(key); deleteErr != nil {
				log.Println(deleteErr)
				return errors.New(apiError.DatabaseError)
			}
		}
	}

	return nil
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

	written, err := repository.RedisDBHandlerInterface.SetIfKeyAbsent(
		userSuspensionKey(data.TenantID, data.UserID),
		data.SessionID,
		encodedStr,
		time.Duration(data.ExpireTimeInSeconds)*time.Second,
	)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}
	if !written {
		return errors.New(apiError.AccountSuspended)
	}

	return nil
}
