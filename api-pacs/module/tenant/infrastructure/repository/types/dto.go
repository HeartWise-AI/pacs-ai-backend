package types

type GetTenant struct {
	ID              string
	Name            string
	Address         string
	AvailableModels []string
	AET             string
	CreatedAt       uint
	UpdatedAt       uint
}
