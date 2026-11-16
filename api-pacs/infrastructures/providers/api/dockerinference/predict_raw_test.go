package dockerinference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-pacs/infrastructures/providers/api/dockerinference/types"
)

func TestPredictForwardsRawRequestUnchanged(t *testing.T) {
	raw := `{"outputMode":"JSON","seriesInstanceImages":{"0":{"0":"QUJD"}},"extraField":[1,2]}`
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		json.NewEncoder(w).Encode(types.PredictResponse{Success: true})
	}))
	defer server.Close()

	api := &DockerInferenceAPI{}
	if _, err := api.Predict(context.Background(), strings.TrimPrefix(server.URL, "http://"), types.PredictRequest{Raw: json.RawMessage(raw)}); err != nil {
		t.Fatal(err)
	}
	if got != raw {
		t.Fatalf("raw request was altered: %q", got)
	}
}
