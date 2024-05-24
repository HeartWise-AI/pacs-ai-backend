package types

type GetTenant struct {
	ID              string
	Name            string
	Address         string
	AvailableModels []map[string]interface{}
	AET             string
	CreatedAt       uint
	UpdatedAt       uint
}
