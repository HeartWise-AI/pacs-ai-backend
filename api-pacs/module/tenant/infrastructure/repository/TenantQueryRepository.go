package repository

import "api-pacs/infrastructures/providers/sdk/firebaseadmin"

// TenantQueryRepository handles the tenant query repository logic
type TenantQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}
