package repository

import (
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

type IAMCommandRepositoryInterface interface {
	DeleteTokenSession(key string) error
	IsEmailVerificationCooldownActive(key string) (bool, error)
	SetEmailVerificationCooldown(key string) error
	SetTokenSession(data repositoryTypes.SetTokenSession) error
}
