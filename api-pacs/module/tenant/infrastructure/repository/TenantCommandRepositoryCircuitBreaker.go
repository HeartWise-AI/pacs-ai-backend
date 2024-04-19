package repository

import (
	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/tenant/domain/repository"
)

// TenantCommandRepositoryCircuitBreaker circuit breaker for tenant command repository
type TenantCommandRepositoryCircuitBreaker struct {
	repository.TenantCommandRepositoryInterface
}

var config = hystrix_config.Config{}
