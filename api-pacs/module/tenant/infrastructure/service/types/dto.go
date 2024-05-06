package types

type GetTenant struct {
	ID              string
	Name            string
	Address         string
	AvailableModels []map[string]interface{}
	CreatedAt       uint
	UpdatedAt       uint
}
