package types

type Config struct {
	ElasticsearchURL string
	Username         string
	Password         string
}

type SearchDocument struct {
	Index     string
	TenantID  string
	Query     string
	StartDate uint
	EndDate   uint
}
