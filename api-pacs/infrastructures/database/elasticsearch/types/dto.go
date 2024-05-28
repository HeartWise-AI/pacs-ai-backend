package types

type Config struct {
	ElasticsearchURL string
}

type SearchParameter struct {
	Index     string
	Query     string
	Fields    []string
	StartDate uint
	EndDate   uint
}
