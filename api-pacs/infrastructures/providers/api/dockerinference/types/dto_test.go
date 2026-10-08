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
			name: "null required fields",
			body: `{
				"gpuRequired": null,
				"residentMemoryMiB": null,
				"peakMemoryMiB": null,
				"maxConcurrentInferences": 1,
				"idleTimeoutSeconds": 600
			}`,
			errorField: "gpuRequired cannot be null",
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
	require.Nil(t, response.Data.Provenance)

	roundTripped, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(roundTripped), `"maxConcurrentInferences":1`)

	var missingResources GetModelInfoResponse
	err = json.Unmarshal([]byte(`{"success":true,"data":{"modelId":"missing"}}`), &missingResources)
	require.ErrorContains(t, err, "resources is required")
}

func TestGetModelInfoResponseRoundTripsOptionalProvenance(t *testing.T) {
	body := `{
		"success": true,
		"message": "ok",
		"data": {
			"modelId": "DeepCORO_SYNTAX",
			"modelName": "DeepCORO-SYNTAX",
			"version": "6.0.0",
			"resources": ` + validGPUResourcesJSON + `,
			"provenance": {
				"sourceRepository": "HeartWise-AI/pacs-ai-backend",
				"sourceRevision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"modelRepository": "heartwise/deepcoro_clip_cardiosyntax",
				"modelRevision": "1ffb38cfc10fa10c4b60f44746063feb860eeba0",
				"weightsPath": "v6_20260929-203527/models/best_model_epoch_19.pt",
				"weightsSha256": "856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0"
			}
		}
	}`

	var response GetModelInfoResponse
	require.NoError(t, json.Unmarshal([]byte(body), &response))
	require.NotNil(t, response.Data.Provenance)
	require.Equal(t, "1ffb38cfc10fa10c4b60f44746063feb860eeba0", response.Data.Provenance.ModelRevision)
	require.Equal(t, "856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0", response.Data.Provenance.WeightsSHA256)

	roundTripped, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(roundTripped), `"modelRevision":"1ffb38cfc10fa10c4b60f44746063feb860eeba0"`)
}

func TestModelProvenanceRejectsIncompleteAndMalformedContracts(t *testing.T) {
	valid := map[string]interface{}{
		"sourceRepository": "HeartWise-AI/pacs-ai-backend",
		"sourceRevision":   nil,
		"modelRepository":  "heartwise/example-model",
		"modelRevision":    strings.Repeat("b", 40),
		"weightsPath":      "release/models/model.pt",
		"weightsSha256":    strings.Repeat("c", 64),
	}

	clone := func() map[string]interface{} {
		copied := make(map[string]interface{}, len(valid))
		for key, value := range valid {
			copied[key] = value
		}
		return copied
	}

	tests := []struct {
		name       string
		mutate     func(map[string]interface{})
		errorField string
	}{
		{
			name: "missing source revision",
			mutate: func(value map[string]interface{}) {
				delete(value, "sourceRevision")
			},
			errorField: "sourceRevision",
		},
		{
			name: "invalid source repository",
			mutate: func(value map[string]interface{}) {
				value["sourceRepository"] = "https://github.com/repository"
			},
			errorField: "sourceRepository",
		},
		{
			name: "invalid source revision",
			mutate: func(value map[string]interface{}) {
				value["sourceRevision"] = strings.Repeat("A", 40)
			},
			errorField: "sourceRevision",
		},
		{
			name: "invalid model revision",
			mutate: func(value map[string]interface{}) {
				value["modelRevision"] = strings.Repeat("b", 39)
			},
			errorField: "modelRevision",
		},
		{
			name: "unsafe absolute path",
			mutate: func(value map[string]interface{}) {
				value["weightsPath"] = "/models/model.pt"
			},
			errorField: "weightsPath",
		},
		{
			name: "unsafe parent path",
			mutate: func(value map[string]interface{}) {
				value["weightsPath"] = "release/../model.pt"
			},
			errorField: "weightsPath",
		},
		{
			name: "invalid checksum",
			mutate: func(value map[string]interface{}) {
				value["weightsSha256"] = strings.Repeat("C", 64)
			},
			errorField: "weightsSha256",
		},
		{
			name: "unknown field",
			mutate: func(value map[string]interface{}) {
				value["unexpected"] = "value"
			},
			errorField: "unexpected",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := clone()
			test.mutate(candidate)
			body, err := json.Marshal(candidate)
			require.NoError(t, err)

			var provenance ModelProvenance
			err = json.Unmarshal(body, &provenance)
			require.ErrorContains(t, err, test.errorField)
		})
	}
}

func TestModelInfoRejectsExplicitNullProvenance(t *testing.T) {
	body := `{
		"modelId": "legacy",
		"resources": ` + validGPUResourcesJSON + `,
		"provenance": null
	}`

	var modelInfo ModelInfo
	err := json.Unmarshal([]byte(body), &modelInfo)
	require.ErrorContains(t, err, "provenance must be omitted")
}
