package types

type DataView struct {
	Title string `json:"title"`
	Name  string `json:"name"`
}

type CreateDataViewRequest struct {
	DataView DataView `json:"data_view"`
}
