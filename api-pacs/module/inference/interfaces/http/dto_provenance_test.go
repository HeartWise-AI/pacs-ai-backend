package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
)

func TestAvailableModelResponsePreservesOptionalProvenance(t *testing.T) {
	sourceRevision := strings.Repeat("a", 40)
	provenance := &dockerInferenceTypes.ModelProvenance{
		SourceRepository: "HeartWise-AI/pacs-ai-backend",
		SourceRevision:   &sourceRevision,
		ModelRepository:  "heartwise/example-model",
		ModelRevision:    strings.Repeat("b", 40),
		WeightsPath:      "release/models/model.pt",
		WeightsSHA256:    strings.Repeat("c", 64),
	}

	encoded, err := json.Marshal(GetInferenceAvailableModelResponse{
		ModelID:    "Example",
		ModelName:  "Example model",
		Version:    "1.0.0",
		Provenance: provenance,
	})
	require.NoError(t, err)

	var decoded GetInferenceAvailableModelResponse
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, provenance, decoded.Provenance)

	legacy, err := json.Marshal(GetInferenceAvailableModelResponse{
		ModelID:   "Legacy",
		ModelName: "Legacy model",
		Version:   "1.0.0",
	})
	require.NoError(t, err)
	require.NotContains(t, string(legacy), `"provenance"`)
}
