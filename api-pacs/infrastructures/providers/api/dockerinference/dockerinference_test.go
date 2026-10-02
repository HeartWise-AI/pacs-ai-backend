package dockerinference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/infrastructures/providers/api/dockerinference/types"
)

func TestConcurrentInferenceRequestsUseRaceSafeClients(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/inference/model-info" {
			_, _ = response.Write([]byte(`{
				"success": true,
				"data": {
					"resources": {
						"gpuRequired": true,
						"residentMemoryMiB": 100,
						"peakMemoryMiB": 200,
						"maxConcurrentInferences": 1,
						"idleTimeoutSeconds": 600
					}
				}
			}`))
			return
		}
		_, _ = response.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)

	containerName := strings.TrimPrefix(server.URL, "http://")
	api := &DockerInferenceAPI{}
	requestErrors := make(chan error, 30)
	var requests sync.WaitGroup

	for index := 0; index < 10; index++ {
		requests.Add(3)
		go func() {
			defer requests.Done()
			_, err := api.GetModelInfo(context.Background(), containerName)
			requestErrors <- err
		}()
		go func() {
			defer requests.Done()
			_, err := api.GetModelFacts(context.Background(), containerName)
			requestErrors <- err
		}()
		go func() {
			defer requests.Done()
			_, err := api.Predict(context.Background(), containerName, types.PredictRequest{})
			requestErrors <- err
		}()
	}

	requests.Wait()
	close(requestErrors)
	for err := range requestErrors {
		require.NoError(t, err)
	}
}

func TestGetModelInfoDecodesAndValidatesResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"success": true,
			"message": "ok",
			"data": {
				"modelId": "DeepCORO_CTO",
				"resources": {
					"gpuRequired": true,
					"residentMemoryMiB": 5744,
					"peakMemoryMiB": 6488,
					"maxConcurrentInferences": 1,
					"idleTimeoutSeconds": 600
				}
			}
		}`))
	}))
	t.Cleanup(server.Close)

	response, err := (&DockerInferenceAPI{}).GetModelInfo(
		context.Background(),
		strings.TrimPrefix(server.URL, "http://"),
	)
	require.NoError(t, err)
	require.Equal(t, 5744, response.Data.Resources.ResidentMemoryMiB)
	require.Equal(t, 6488, response.Data.Resources.PeakMemoryMiB)
}

func TestGetModelInfoRejectsMissingResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"data":{"modelId":"invalid"}}`))
	}))
	t.Cleanup(server.Close)

	_, err := (&DockerInferenceAPI{}).GetModelInfo(
		context.Background(),
		strings.TrimPrefix(server.URL, "http://"),
	)
	require.Error(t, err)
}
