package service

import (
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/module/tenant/domain/repository"
)

// TenantCommandService handles the Tenant command service logic
type TenantCommandService struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	repository.TenantCommandRepositoryInterface
}
