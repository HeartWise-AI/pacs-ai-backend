package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const validGPUResourcesJSON = `{
	"gpuRequired": true,
	"residentMemoryMiB": 5744,
	"peakMemoryMiB": 6488,
	"maxConcurrentInferences": 1,
	"idleTimeoutSeconds": 600
}`

func TestModelResourcesUnmarshalValidContracts(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "GPU", body: validGPUResourcesJSON},
		{name: "CPU", body: `{
			"gpuRequired": false,
			"residentMemoryMiB": 0,
			"peakMemoryMiB": 0,
			"maxConcurrentInferences": 1,
			"idleTimeoutSeconds": 600
		}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var resources ModelResources
			require.NoError(t, json.Unmarshal([]byte(test.body), &resources))
			require.NoError(t, resources.Validate())
		})
	}
}

func TestModelResourcesUnmarshalRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		errorField string
	}{
		{
			name:       "missing field",
			body:       strings.Replace(validGPUResourcesJSON, `"peakMemoryMiB": 6488,`, "", 1),
			errorField: "peakMemoryMiB",
		},
		{
			name:       "string integer",
			body:       strings.Replace(validGPUResourcesJSON, `"residentMemoryMiB": 5744`, `"residentMemoryMiB": "5744"`, 1),
			errorField: "residentMemoryMiB",
		},
		{
			name:       "floating point integer",
			body:       strings.Replace(validGPUResourcesJSON, `"peakMemoryMiB": 6488`, `"peakMemoryMiB": 6488.5`, 1),
			errorField: "peakMemoryMiB",
		},
		{
			name:       "zero resident GPU memory",
			body:       strings.Replace(validGPUResourcesJSON, `"residentMemoryMiB": 5744`, `"residentMemoryMiB": 0`, 1),
			errorField: "residentMemoryMiB",
		},
		{
			name:       "negative peak GPU memory",
			body:       strings.Replace(validGPUResourcesJSON, `"peakMemoryMiB": 6488`, `"peakMemoryMiB": -1`, 1),
			errorField: "peakMemoryMiB",
		},
		{
			name:       "peak below resident",
			body:       strings.Replace(validGPUResourcesJSON, `"peakMemoryMiB": 6488`, `"peakMemoryMiB": 5000`, 1),
			errorField: "peakMemoryMiB",
		},
		{
			name:       "zero concurrency",
			body:       strings.Replace(validGPUResourcesJSON, `"maxConcurrentInferences": 1`, `"maxConcurrentInferences": 0`, 1),
			errorField: "maxConcurrentInferences",
		},
		{
			name:       "unsupported V1 concurrency",
			body:       strings.Replace(validGPUResourcesJSON, `"maxConcurrentInferences": 1`, `"maxConcurrentInferences": 2`, 1),
			errorField: "maxConcurrentInferences",
		},
		{
			name:       "zero idle timeout",
			body:       strings.Replace(validGPUResourcesJSON, `"idleTimeoutSeconds": 600`, `"idleTimeoutSeconds": 0`, 1),
			errorField: "idleTimeoutSeconds",
		},
		{
			name: "CPU with GPU memory",
			body: strings.Replace(
				strings.Replace(validGPUResourcesJSON, `"gpuRequired": true`, `"gpuRequired": false`, 1),
				`"peakMemoryMiB": 6488`, `"peakMemoryMiB": 5744`, 1,
			),
			errorField: "GPU memory",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var resources ModelResources
			err := json.Unmarshal([]byte(test.body), &resources)
			require.Error(t, err)
			require.Contains(t, err.Error(), test.errorField)
		})
	}
}

func TestModelResourcesRequiresEveryField(t *testing.T) {
	var valid map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(validGPUResourcesJSON), &valid))

	for _, field := range []string{
		"gpuRequired",
		"residentMemoryMiB",
		"peakMemoryMiB",
		"maxConcurrentInferences",
		"idleTimeoutSeconds",
	} {
		t.Run(field, func(t *testing.T) {
			missingField := make(map[string]interface{}, len(valid)-1)
			for key, value := range valid {
				if key != field {
					missingField[key] = value
				}
			}
			body, err := json.Marshal(missingField)
			require.NoError(t, err)

			var resources ModelResources
			err = json.Unmarshal(body, &resources)
			require.ErrorContains(t, err, field)
		})
	}
}

func TestGetModelInfoResponseRequiresAndRoundTripsResources(t *testing.T) {
	body := `{
		"success": true,
		"message": "ok",
		"data": {
			"modelId": "DeepCORO_CTO",
			"modelName": "DeepCORO-CTO",
			"version": "2.0.0",
			"resources": ` + validGPUResourcesJSON + `
		}
	}`

	var response GetModelInfoResponse
	require.NoError(t, json.Unmarshal([]byte(body), &response))
	require.Equal(t, 5744, response.Data.Resources.ResidentMemoryMiB)
	require.Equal(t, 6488, response.Data.Resources.PeakMemoryMiB)

	roundTripped, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(roundTripped), `"maxConcurrentInferences":1`)

	var missingResources GetModelInfoResponse
	err = json.Unmarshal([]byte(`{"success":true,"data":{"modelId":"missing"}}`), &missingResources)
	require.ErrorContains(t, err, "resources is required")
}
