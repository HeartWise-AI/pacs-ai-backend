package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	types "api-pacs/infrastructures/providers/api/inference/types"
	apiError "api-pacs/internal/errors"
)

type InferenceAPI struct {
	BaseURL string
}

var (
	client *http.Client = &http.Client{Timeout: 5 * time.Minute}
)

func Init(baseURL string) *InferenceAPI {
	return &InferenceAPI{
		BaseURL: baseURL,
	}
}

// DetectVessel detect vessel (X3D_1) from inference API
func (t *InferenceAPI) DetectVessel(ctx context.Context, instances types.Instances) ([][]float64, error) {
	result, err := sendDetectionRequest(fmt.Sprintf("%s/predictions/X3D_1", t.BaseURL), instances)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// DetectLVEF detect LVEF (X3D_2) from inference API
func (t *InferenceAPI) DetectLVEF(ctx context.Context, instances types.Instances) ([][]float64, error) {
	result, err := sendDetectionRequest(fmt.Sprintf("%s/predictions/X3D_2", t.BaseURL), instances)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func sendDetectionRequest(inferenceURL string, instances types.Instances) ([][]float64, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(instances)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, inferenceURL, buf)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// convert body bytes to string
	bodyString := string(bodyBytes)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Println("Error:", bodyString)
		return nil, errors.New(apiError.TorchServeError)
	}

	// parse the response into a 2D slice of float64
	var result [][]float64
	err = json.Unmarshal(bodyBytes, &result)
	if err != nil {
		return nil, err
	}

	return result, nil
}
