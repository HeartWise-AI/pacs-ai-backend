package service

import (
	"context"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type allowAllInferenceQuotaManager struct{}

func (manager *allowAllInferenceQuotaManager) Reserve(context.Context, serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	return serviceTypes.InferenceQuotaStatus{Allowance: 100, Remaining: 99, MaxConcurrentExecutions: 10, ActiveExecutions: 1}, nil
}

func (manager *allowAllInferenceQuotaManager) Release(context.Context, serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	return serviceTypes.InferenceQuotaStatus{Allowance: 100, Remaining: 99, MaxConcurrentExecutions: 10}, nil
}

func (manager *allowAllInferenceQuotaManager) Refund(context.Context, serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	return serviceTypes.InferenceQuotaStatus{Allowance: 100, Remaining: 100, MaxConcurrentExecutions: 10}, nil
}

func (manager *allowAllInferenceQuotaManager) Status(context.Context, string, string) (serviceTypes.InferenceQuotaStatus, error) {
	return serviceTypes.InferenceQuotaStatus{Allowance: 100, Remaining: 100, MaxConcurrentExecutions: 10}, nil
}
