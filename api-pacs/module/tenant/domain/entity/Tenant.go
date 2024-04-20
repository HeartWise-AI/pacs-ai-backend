package entity

// Tenant holds the tenant entity fields
type Tenant struct {
	ID        string `json:"id,omitempty"` // firestore document id
	Name      string `json:"name"`
	Address   string `json:"address"`
	CreatedAt uint   `json:"created_at"`
	UpdatedAt uint   `json:"updated_at"`
}

// GetModelName returns the model name of tenant entity that can be used for naming schemas
func (entity *Tenant) GetModelName() string {
	return "tenants"
}
