package repository

import (
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

type IAMCommandRepositoryInterface interface {
	DeleteTokenSession(key string) error
	SetTokenSession(data repositoryTypes.SetTokenSession) error
}
