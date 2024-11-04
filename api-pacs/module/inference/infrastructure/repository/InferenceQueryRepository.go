package repository

import (
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
)

// InferenceQueryRepository handles the inference query repository logic
type InferenceQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}
