package service

import "api-pacs/module/tenant/domain/repository"

// TenantQueryService handles the Tenant query service logic
type TenantQueryService struct {
	repository.TenantQueryRepositoryInterface
}
