package entity

import (
	"strings"
	"testing"
)

func validInferenceModelVersion() InferenceModelVersion {
	sourceRevision := strings.Repeat("a", 40)
	return InferenceModelVersion{
		ModelID:        "DeepCORO-SYNTAX",
		Version:        "6.0.0",
		ImageReference: "heartwisehub/pacs-ai-deepcoro-syntax:6.0.0",
		ImageDigest:    "sha256:" + strings.Repeat("b", 64),
		Status:         InferenceModelVersionAvailable,
		Provenance: &InferenceModelProvenance{
			SourceRepository: "HeartWise-AI/pacs-ai-backend",
			SourceRevision:   &sourceRevision,
			ModelRepository:  "HeartWise-AI/DeepCORO-SYNTAX",
			ModelRevision:    strings.Repeat("c", 40),
			WeightsPath:      "weights/best_model.pt",
			WeightsSHA256:    strings.Repeat("d", 64),
		},
	}
}

func TestInferenceModelVersionValidate(t *testing.T) {
	valid := validInferenceModelVersion()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		change func(*InferenceModelVersion)
	}{
		{name: "model ID", change: func(value *InferenceModelVersion) { value.ModelID = "bad/model" }},
		{name: "version", change: func(value *InferenceModelVersion) { value.Version = "v6" }},
		{name: "image tag", change: func(value *InferenceModelVersion) { value.ImageReference = "heartwisehub/model:5.0.0" }},
		{name: "digest", change: func(value *InferenceModelVersion) { value.ImageDigest = "sha256:bad" }},
		{name: "status", change: func(value *InferenceModelVersion) { value.Status = "UNKNOWN" }},
		{name: "provenance", change: func(value *InferenceModelVersion) { value.Provenance = nil }},
		{name: "source revision", change: func(value *InferenceModelVersion) { value.Provenance.SourceRevision = nil }},
		{name: "weights path", change: func(value *InferenceModelVersion) { value.Provenance.WeightsPath = "../model.pt" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validInferenceModelVersion()
			test.change(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid model version was accepted")
			}
		})
	}
}

func TestInferenceModelVersionIDIsStableAndVersionSpecific(t *testing.T) {
	first := InferenceModelVersionID(" model-a ", " 1.0.0 ")
	if first != InferenceModelVersionID("model-a", "1.0.0") {
		t.Fatal("document ID is not normalization-stable")
	}
	if first == InferenceModelVersionID("model-a", "2.0.0") {
		t.Fatal("different versions received the same document ID")
	}
	if len(first) != 64 {
		t.Fatalf("document ID length = %d", len(first))
	}
}
