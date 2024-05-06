package service

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"api-pacs/module/tenant/domain/repository"
	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantQueryService handles the Tenant query service logic
type TenantQueryService struct {
	repository.TenantQueryRepositoryInterface
}

// GetTenantByID get tenant by id
func (service *TenantQueryService) GetTenantByID(ctx context.Context, tenantID string) (types.GetTenant, error) {
	tenant, err := service.TenantQueryRepositoryInterface.SelectTenantByID(ctx, tenantID)
	if err != nil {
		return types.GetTenant{}, err
	}
	rootPath, _ := os.Getwd()
	storagePath := filepath.Join(rootPath, "storage")

	availableModels := []map[string]interface{}{}

	for _, model := range tenant.AvailableModels {
		jsonData, err := os.ReadFile(filepath.Join(storagePath, model+".json"))
		if err != nil {
			log.Print(err)
			continue
		}

		var parsedModel map[string]interface{}

		err = json.Unmarshal(jsonData, &parsedModel)
		if err != nil {
			log.Print(err)
			return types.GetTenant{}, err
		}

		availableModels = append(availableModels, parsedModel)
	}

	return types.GetTenant{
		ID:              tenant.ID,
		Name:            tenant.Name,
		Address:         tenant.Address,
		AvailableModels: availableModels,
		CreatedAt:       uint(tenant.CreatedAt),
		UpdatedAt:       uint(tenant.UpdatedAt),
	}, nil
}
