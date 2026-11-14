package entity

// UserMetadata holds the user metadata entity fields
type UserMetadata struct {
	UserID    string `firestore:"user_id"`
	Metadata  string `firestore:"metadata"` // json
	CreatedAt int    `firestore:"created_at,omitempty"`
	UpdatedAt int    `firestore:"updated_at"`
	// ViewerTourCompletedAt is the unix time (seconds) at which the user completed or
	// skipped the viewer onboarding tour; nil means the tour should be shown. It is a
	// separate document field (not part of the Metadata JSON blob) so full-blob
	// metadata updates cannot clobber it.
	ViewerTourCompletedAt *int64 `firestore:"viewer_tour_completed_at,omitempty"`
}

// GetModelName returns the model name of user entity that can be used for naming schemas
func (entity *UserMetadata) GetModelName() string {
	return "user_metadata"
}
