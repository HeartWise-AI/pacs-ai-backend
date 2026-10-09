package modelupgrade

import (
	"strings"
	"testing"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

func TestValidateImageReferenceRequiresAllowedVersionedArtifact(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		digest    string
		valid     bool
	}{
		{name: "versioned tag", reference: "heartwisehub/model:2.0.0", valid: true},
		{name: "tag and digest", reference: "heartwisehub/model:2.0.0@" + testDigest, digest: testDigest, valid: true},
		{name: "immutable digest", reference: "heartwisehub/model@" + testDigest, valid: true},
		{name: "missing tag", reference: "heartwisehub/model"},
		{name: "latest", reference: "heartwisehub/model:latest"},
		{name: "build metadata", reference: "heartwisehub/model:2.0.0+cuda"},
		{name: "unapproved namespace", reference: "someone-else/model:2.0.0"},
		{name: "unapproved registry", reference: "evil.example/heartwisehub/model:2.0.0"},
		{name: "non sha256", reference: "heartwisehub/model:2.0.0", digest: "md5:abc"},
		{name: "digest disagreement", reference: "heartwisehub/model:2.0.0@" + testDigest, digest: "sha256:" + strings.Repeat("1", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateImageReference(test.reference, test.digest, "heartwisehub")
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, err=%v", test.valid, err)
			}
		})
	}
}

func TestResolveImageDigestPinsRequestedRepository(t *testing.T) {
	image, err := validateImageReference("heartwisehub/model:2.0.0", testDigest, "heartwisehub")
	if err != nil {
		t.Fatal(err)
	}
	inspection := docker.InspectImageResult{RepoDigests: []string{
		"heartwisehub/other@" + testDigest,
		"heartwisehub/model@sha256:" + strings.Repeat("1", 64),
		"heartwisehub/model@" + testDigest,
	}}
	resolved, err := resolveImageDigest(image, inspection)
	if err != nil || resolved != testDigest {
		t.Fatalf("unexpected resolution: digest=%s err=%v", resolved, err)
	}
}

func TestValidateCandidateIdentityRequiresMatchingRuntimeAndOCILabels(t *testing.T) {
	image, err := validateImageReference("heartwisehub/model:2.0.0", testDigest, "heartwisehub")
	if err != nil {
		t.Fatal(err)
	}
	sourceRevision := testSourceSHA
	active := api.ModelInfo{ModelID: "model-1", Version: "1.0.0"}
	candidate := api.ModelInfo{
		ModelID: "model-1", Version: "2.0.0", SupportedOutputModes: []string{"JSON", "HTML"},
		Resources: api.ModelResources{MaxConcurrentInferences: 1, IdleTimeoutSeconds: 60},
		Provenance: &api.ModelProvenance{
			SourceRepository: "HeartWise-AI/pacs-ai-backend", SourceRevision: &sourceRevision,
			ModelRepository: "HeartWise-AI/model", ModelRevision: testModelSHA,
			WeightsPath: "weights/model.pt", WeightsSHA256: testWeightsSHA,
		},
	}
	inspection := newFakeUpgradeDocker().inspection
	if _, err = validateCandidateIdentity(image, testDigest, inspection, active, candidate, entity.OutputModeHTML); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}

	t.Run("model id", func(t *testing.T) {
		changed := candidate
		changed.ModelID = "another-model"
		if _, validationErr := validateCandidateIdentity(image, testDigest, inspection, active, changed, entity.OutputModeHTML); validationErr == nil {
			t.Fatal("mismatched model id was accepted")
		}
	})
	t.Run("non increasing version", func(t *testing.T) {
		changed := candidate
		changed.Version = active.Version
		changedInspection := inspection
		changedInspection.Labels = cloneLabels(inspection.Labels)
		changedInspection.Labels["org.opencontainers.image.version"] = changed.Version
		if _, validationErr := validateCandidateIdentity(image, testDigest, changedInspection, active, changed, entity.OutputModeHTML); validationErr == nil {
			t.Fatal("non-increasing version was accepted")
		}
	})
	t.Run("source label", func(t *testing.T) {
		changedInspection := inspection
		changedInspection.Labels = cloneLabels(inspection.Labels)
		changedInspection.Labels["org.opencontainers.image.source"] = "https://github.com/other/repository"
		if _, validationErr := validateCandidateIdentity(image, testDigest, changedInspection, active, candidate, entity.OutputModeHTML); validationErr == nil {
			t.Fatal("mismatched source label was accepted")
		}
	})
	t.Run("registered output mode", func(t *testing.T) {
		changed := candidate
		changed.SupportedOutputModes = []string{"JSON"}
		if _, validationErr := validateCandidateIdentity(image, testDigest, inspection, active, changed, entity.OutputModeHTML); validationErr == nil {
			t.Fatal("candidate without the registered output mode was accepted")
		}
	})
}

func TestNewerSemanticVersionFollowsPrereleasePrecedence(t *testing.T) {
	tests := []struct {
		active, candidate string
		newer             bool
	}{
		{active: "1.9.9", candidate: "2.0.0", newer: true},
		{active: "2.0.0-rc.2", candidate: "2.0.0-rc.10", newer: true},
		{active: "2.0.0-rc.10", candidate: "2.0.0", newer: true},
		{active: "2.0.0", candidate: "2.0.0-rc.10", newer: false},
		{active: "2.0.0", candidate: "2.0.0", newer: false},
		{active: "not-semver", candidate: "2.0.0", newer: false},
	}
	for _, test := range tests {
		if got := newerSemanticVersion(test.active, test.candidate); got != test.newer {
			t.Fatalf("newerSemanticVersion(%q, %q)=%v want %v", test.active, test.candidate, got, test.newer)
		}
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}
