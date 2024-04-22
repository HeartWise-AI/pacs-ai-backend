package repository

import (
	"api-pacs/module/tenant/domain/repository"
)

// TenantQueryRepositoryCircuitBreaker is the circuit breaker for the tenant query repository
type TenantQueryRepositoryCircuitBreaker struct {
	repository.TenantQueryRepositoryInterface
}
