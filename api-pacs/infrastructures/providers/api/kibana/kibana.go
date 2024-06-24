package kibana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"api-pacs/infrastructures/providers/api/kibana/types"
)

type KibanaAPI struct {
	BaseURL string
}

var (
	client *http.Client = &http.Client{Timeout: 5 * time.Minute}
)

func Init(baseURL string) *KibanaAPI {
	return &KibanaAPI{
		BaseURL: baseURL,
	}
}

// CreateDataView create data view
func (k *KibanaAPI) CreateDataView(ctx context.Context, requestPayload types.DataView) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(types.CreateDataViewRequest{
		DataView: types.DataView{
			Title: requestPayload.Title,
			Name:  requestPayload.Name,
		},
	})
	if err != nil {
		return nil
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/data_views/data_view", k.BaseURL), buf)
	if err != nil {
		return err
	}

	req.Header.Set("kbn-xsrf", "true")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return err
	}

	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	responseMessage := string(response)
	log.Println("Success:", responseMessage)

	return nil
}
