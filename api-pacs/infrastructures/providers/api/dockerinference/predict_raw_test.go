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

func TestPredictStreamsRequestWithExactContentLength(t *testing.T) {
	raw := `{"outputMode":"JSON","seriesInstanceImages":{"0":{"0":"QUJD"}}}`
	var got string
	var contentLength int64
	var chunked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got, contentLength, chunked = string(body), r.ContentLength, len(r.TransferEncoding) > 0
		json.NewEncoder(w).Encode(types.PredictResponse{Success: true})
	}))
	defer server.Close()

	api := &DockerInferenceAPI{}
	host := strings.TrimPrefix(server.URL, "http://")
	// iotest-style reader: no length the HTTP client could discover by itself.
	request := types.PredictRequest{Stream: io.MultiReader(strings.NewReader(raw)), StreamLength: int64(len(raw))}
	if _, err := api.Predict(context.Background(), host, request); err != nil {
		t.Fatal(err)
	}
	if got != raw || contentLength != int64(len(raw)) || chunked {
		t.Fatalf("streamed request was altered: %q length=%d chunked=%t", got, contentLength, chunked)
	}
	// A source shorter than announced fails instead of sending a short body.
	short := types.PredictRequest{Stream: strings.NewReader(raw[:10]), StreamLength: int64(len(raw))}
	if _, err := api.Predict(context.Background(), host, short); err == nil {
		t.Fatal("a truncated stream must fail the prediction")
	}
	if _, err := api.Predict(context.Background(), host, types.PredictRequest{Stream: strings.NewReader(raw)}); err == nil {
		t.Fatal("a stream without a length must be refused")
	}
}
