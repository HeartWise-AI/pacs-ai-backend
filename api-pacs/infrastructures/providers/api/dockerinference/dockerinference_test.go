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
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
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
