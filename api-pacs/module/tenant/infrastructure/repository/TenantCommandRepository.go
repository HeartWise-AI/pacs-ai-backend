package repository

import (
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
)

// TenantCommandRepository handles the tenant command repository logic
type TenantCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}
