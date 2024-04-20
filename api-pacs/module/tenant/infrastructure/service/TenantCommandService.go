package service

import (
	"github.com/segmentio/ksuid"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/module/tenant/domain/repository"
)

// TenantCommandService handles the Tenant command service logic
type TenantCommandService struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	repository.TenantCommandRepositoryInterface
}

// TODO: VerifyTenantTenantEmail

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
