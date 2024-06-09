package types

type Config struct {
	ElasticsearchURL string
}

type SearchDocument struct {
	Index     string
	Query     string
	StartDate uint
	EndDate   uint
}
