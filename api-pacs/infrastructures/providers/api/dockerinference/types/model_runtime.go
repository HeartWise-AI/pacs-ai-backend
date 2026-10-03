package types

import (
	"encoding/json"
	"fmt"
	"time"
)

type ModelRuntimeState string

const (
	RuntimeUnloaded ModelRuntimeState = "UNLOADED"
	RuntimeLoading  ModelRuntimeState = "LOADING"
	RuntimeReady    ModelRuntimeState = "READY"
	RuntimeBusy     ModelRuntimeState = "BUSY"
	RuntimeEvicting ModelRuntimeState = "EVICTING"
	RuntimeError    ModelRuntimeState = "ERROR"
)

type ModelRuntime struct {
	State          ModelRuntimeState `json:"state"`
	Loaded         bool              `json:"loaded"`
	ActiveRequests int               `json:"activeRequests"`
	LastUsedAt     *time.Time        `json:"lastUsedAt"`
	Error          string            `json:"error,omitempty"`
}

func (r *ModelRuntime) UnmarshalJSON(data []byte) error {
	type alias ModelRuntime
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"state", "loaded", "activeRequests", "lastUsedAt"} {
		value, ok := fields[name]
		if !ok || (name != "lastUsedAt" && string(value) == "null") {
			return fmt.Errorf("runtime.%s is required", name)
		}
	}
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = ModelRuntime(decoded)
	return r.Validate()
}
func (r ModelRuntime) Validate() error {
	switch r.State {
	case RuntimeUnloaded, RuntimeLoading, RuntimeReady, RuntimeBusy, RuntimeEvicting, RuntimeError:
	default:
		return fmt.Errorf("unknown model runtime state %q", r.State)
	}
	if r.ActiveRequests < 0 || r.ActiveRequests > 1 {
		return fmt.Errorf("runtime activeRequests must be 0 or 1")
	}
	if r.State == RuntimeUnloaded && (r.Loaded || r.ActiveRequests != 0) {
		return fmt.Errorf("unloaded runtime cannot be loaded or active")
	}
	if r.State == RuntimeReady && (!r.Loaded || r.ActiveRequests != 0) {
		return fmt.Errorf("ready runtime must be loaded and idle")
	}
	return nil
}

type ModelRuntimeResponse struct {
	Success bool         `json:"success"`
	Data    ModelRuntime `json:"data"`
}
