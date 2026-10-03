package dockerinference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

var lifecycleClient = &http.Client{Timeout: 5 * time.Minute}
var lifecycleHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(?::[0-9]+)?$`)

func (d *DockerInferenceAPI) GetModelRuntime(ctx context.Context, name string) (types.ModelRuntime, error) {
	return d.lifecycle(ctx, name, "runtime", http.MethodGet, metadataClient)
}
func (d *DockerInferenceAPI) LoadModel(ctx context.Context, name string) (types.ModelRuntime, error) {
	return d.lifecycle(ctx, name, "load", http.MethodPost, lifecycleClient)
}
func (d *DockerInferenceAPI) UnloadModel(ctx context.Context, name string) (types.ModelRuntime, error) {
	return d.lifecycle(ctx, name, "unload", http.MethodPost, lifecycleClient)
}
func (d *DockerInferenceAPI) lifecycle(ctx context.Context, name, operation, method string, client *http.Client) (types.ModelRuntime, error) {
	if !lifecycleHost.MatchString(name) {
		return types.ModelRuntime{}, fmt.Errorf("invalid inference container hostname")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+name+"/api/inference/model/"+operation, nil)
	if err != nil {
		return types.ModelRuntime{}, err
	}
	// Do not allow a model to redirect lifecycle requests to another destination.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return types.ModelRuntime{}, fmt.Errorf("model %s: %w", operation, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return types.ModelRuntime{}, fmt.Errorf("model %s HTTP status %d", operation, resp.StatusCode)
	}
	var body types.ModelRuntimeResponse
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return types.ModelRuntime{}, fmt.Errorf("model %s response: %w", operation, err)
	}
	if !body.Success {
		return body.Data, fmt.Errorf("model %s reported failure", operation)
	}
	if err = body.Data.Validate(); err != nil {
		return body.Data, err
	}
	return body.Data, nil
}
