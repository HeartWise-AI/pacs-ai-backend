package repository

import (
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
)

// InferenceCommandRepository handles the inference command repository logic
type InferenceCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}
