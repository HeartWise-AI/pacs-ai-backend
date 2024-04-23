package entity

// Tenant holds the tenant entity fields
type Tenant struct {
	ID        string `firestore:"id,omitempty"`
	Name      string `firestore:"name"`
	Address   string `firestore:"address"`
	CreatedAt uint   `firestore:"created_at"`
	UpdatedAt uint   `firestore:"updated_at"`
}

// GetModelName returns the model name of tenant entity that can be used for naming schemas
func (entity *Tenant) GetModelName() string {
	return "tenants"
}
