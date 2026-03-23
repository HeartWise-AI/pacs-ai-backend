package entity

// UserMetadata holds the user metadata entity fields
type UserMetadata struct {
	UserID    string `firestore:"user_id"`
	Metadata  string `firestore:"metadata"` // json
	CreatedAt int    `firestore:"created_at,omitempty"`
	UpdatedAt int    `firestore:"updated_at"`
}

// GetModelName returns the model name of user entity that can be used for naming schemas
func (entity *UserMetadata) GetModelName() string {
	return "user_metadata"
}
