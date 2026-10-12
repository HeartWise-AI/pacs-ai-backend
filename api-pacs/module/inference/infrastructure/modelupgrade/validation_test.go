package modelupgrade

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

const (
	testImageID     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigest      = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testSourceSHA   = "cccccccccccccccccccccccccccccccccccccccc"
	testModelSHA    = "dddddddddddddddddddddddddddddddddddddddd"
	testWeightsSHA  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	testCandidateID = "candidate-container-id"
)

type validationDocker struct {
	inspection docker.InspectImageResult
	inspectErr error
	info       docker.GetContainerInfoResult
	infoErr    error
	starts     int
}

func (fake *validationDocker) InspectImage(context.Context, string) (docker.InspectImageResult, error) {
	return fake.inspection, fake.inspectErr
}

func (fake *validationDocker) GetContainerInfo(context.Context, string) (docker.GetContainerInfoResult, error) {
	return fake.info, fake.infoErr
}

func (fake *validationDocker) StartContainer(context.Context, string) error {
	fake.starts++
	return nil
}

type validationAPI struct {
	modelInfo  api.GetModelInfoResponse
	modelErr   error
	runtime    api.ModelRuntime
	runtimeErr error
}

func (fake *validationAPI) GetModelInfo(context.Context, string) (api.GetModelInfoResponse, error) {
	return fake.modelInfo, fake.modelErr
}

func (fake *validationAPI) GetModelRuntime(context.Context, string) (api.ModelRuntime, error) {
	return fake.runtime, fake.runtimeErr
}

func TestValidateImageReferenceRequiresAllowedImmutableArtifact(t *testing.T) {
	validator := newTestValidator(t, &validationDocker{}, &validationAPI{})
	tests := []struct {
		name      string
		reference string
		digest    string
		valid     bool
	}{
		{name: "versioned tag", reference: "heartwisehub/model:2.0.0", valid: true},
		{name: "prerelease tag", reference: "heartwisehub/model:2.0.0-rc.1", valid: true},
		{name: "tag and digest", reference: "heartwisehub/model:2.0.0@" + testDigest, digest: testDigest, valid: true},
		{name: "digest only", reference: "heartwisehub/model@" + testDigest, valid: true},
		{name: "missing tag", reference: "heartwisehub/model"},
		{name: "latest", reference: "heartwisehub/model:latest"},
		{name: "build metadata", reference: "heartwisehub/model:2.0.0+cuda"},
		{name: "leading zero prerelease", reference: "heartwisehub/model:2.0.0-rc.01"},
		{name: "unapproved namespace", reference: "someone-else/model:2.0.0"},
		{name: "unapproved registry", reference: "evil.example/heartwisehub/model:2.0.0"},
		{name: "invalid expected digest", reference: "heartwisehub/model:2.0.0", digest: "md5:abc"},
		{name: "non sha256 reference", reference: "heartwisehub/model@sha512:" + strings.Repeat("1", 128)},
		{name: "digest disagreement", reference: "heartwisehub/model:2.0.0@" + testDigest, digest: "sha256:" + strings.Repeat("1", 64)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validator.ValidateImageReference(test.reference, test.digest)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
			if !test.valid && !errors.Is(err, ErrCandidateImageReference) {
				t.Fatalf("error category = %v", err)
			}
		})
	}
}

func TestResolveImagePinsLocalIDAndRequestedRepositoryDigest(t *testing.T) {
	dockerFake := &validationDocker{inspection: validInspection()}
	validator := newTestValidator(t, dockerFake, &validationAPI{})

	resolved, err := validator.ResolveImage(context.Background(), "heartwisehub/model:2.0.0", testDigest)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Reference != "heartwisehub/model:2.0.0" || resolved.Tag != "2.0.0" {
		t.Fatalf("resolved reference = %+v", resolved)
	}
	if resolved.LocalImageID != testImageID || resolved.Digest != testDigest {
		t.Fatalf("resolved identity = %+v", resolved)
	}
}

func TestResolveImageRejectsMismatchedOrIncompleteIdentity(t *testing.T) {
	tests := []struct {
		name       string
		inspection docker.InspectImageResult
	}{
		{
			name: "wrong repository digest",
			inspection: docker.InspectImageResult{
				ID: testImageID, RepoDigests: []string{"heartwisehub/other@" + testDigest},
			},
		},
		{
			name: "wrong digest",
			inspection: docker.InspectImageResult{
				ID: testImageID, RepoDigests: []string{"heartwisehub/model@sha256:" + strings.Repeat("1", 64)},
			},
		},
		{
			name: "invalid local image id",
			inspection: docker.InspectImageResult{
				ID: "not-an-image-id", RepoDigests: []string{"heartwisehub/model@" + testDigest},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validator := newTestValidator(t, &validationDocker{inspection: test.inspection}, &validationAPI{})
			_, err := validator.ResolveImage(context.Background(), "heartwisehub/model:2.0.0", testDigest)
			if !errors.Is(err, ErrCandidateImageIdentity) {
				t.Fatalf("expected image identity error, got %v", err)
			}
		})
	}
}

func TestValidateContainerAcceptsMatchingIdleCandidate(t *testing.T) {
	dockerFake := &validationDocker{info: docker.GetContainerInfoResult{
		ID: testCandidateID, ImageID: testImageID, Name: "/candidate-model", Lifecycle: docker.LifecycleStopped,
	}}
	apiFake := &validationAPI{
		modelInfo: api.GetModelInfoResponse{Success: true, Data: validCandidateModelInfo()},
		runtime:   api.ModelRuntime{State: api.RuntimeUnloaded},
	}
	validator := newTestValidator(t, dockerFake, apiFake)

	result, err := validator.ValidateContainer(context.Background(), ValidateCandidateContainer{
		ContainerID:          testCandidateID,
		Image:                validResolvedImage(),
		ActiveModelInfo:      api.ModelInfo{ModelID: "model-1", Version: "1.0.0"},
		RegisteredOutputMode: entity.OutputModeHTML,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dockerFake.starts != 1 {
		t.Fatalf("candidate start count = %d", dockerFake.starts)
	}
	if result.ContainerID != testCandidateID || result.ContainerName != "candidate-model" {
		t.Fatalf("validated candidate = %+v", result)
	}
	if result.ImageDigest != testDigest || result.ModelInfo.Version != "2.0.0" {
		t.Fatalf("validated identity = %+v", result)
	}
}

func TestValidateContainerRejectsContainerCreatedFromDifferentImage(t *testing.T) {
	dockerFake := &validationDocker{info: docker.GetContainerInfoResult{
		ID:      testCandidateID,
		ImageID: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		Name:    "/candidate-model", Lifecycle: docker.LifecycleStopped,
	}}
	validator := newTestValidator(t, dockerFake, &validationAPI{})

	_, err := validator.ValidateContainer(context.Background(), ValidateCandidateContainer{
		ContainerID:          testCandidateID,
		Image:                validResolvedImage(),
		ActiveModelInfo:      api.ModelInfo{ModelID: "model-1", Version: "1.0.0"},
		RegisteredOutputMode: entity.OutputModeHTML,
	})
	if !errors.Is(err, ErrCandidateImageIdentity) {
		t.Fatalf("expected candidate image identity error, got %v", err)
	}
	if dockerFake.starts != 0 {
		t.Fatal("mismatched candidate was started")
	}
}

func TestValidateCandidateIdentityRejectsContractMismatch(t *testing.T) {
	active := api.ModelInfo{ModelID: "model-1", Version: "1.0.0"}
	tests := []struct {
		name   string
		change func(*ResolvedCandidateImage, *api.ModelInfo)
	}{
		{name: "model ID", change: func(_ *ResolvedCandidateImage, info *api.ModelInfo) { info.ModelID = "other-model" }},
		{name: "missing provenance", change: func(_ *ResolvedCandidateImage, info *api.ModelInfo) { info.Provenance = nil }},
		{name: "invalid resources", change: func(_ *ResolvedCandidateImage, info *api.ModelInfo) { info.Resources.MaxConcurrentInferences = 2 }},
		{name: "registered output mode", change: func(_ *ResolvedCandidateImage, info *api.ModelInfo) { info.SupportedOutputModes = []string{"JSON"} }},
		{name: "image tag", change: func(image *ResolvedCandidateImage, _ *api.ModelInfo) { image.Tag = "3.0.0" }},
		{name: "non-increasing version", change: func(image *ResolvedCandidateImage, info *api.ModelInfo) {
			info.Version = "1.0.0"
			image.Tag = "1.0.0"
			image.labels["org.opencontainers.image.version"] = "1.0.0"
		}},
		{name: "source label", change: func(image *ResolvedCandidateImage, _ *api.ModelInfo) {
			image.labels["org.opencontainers.image.source"] = "https://github.com/other/repository"
		}},
		{name: "weights label", change: func(image *ResolvedCandidateImage, _ *api.ModelInfo) {
			image.labels["ai.heartwise.model.weights.sha256"] = strings.Repeat("f", 64)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image := validResolvedImage()
			candidate := validCandidateModelInfo()
			test.change(&image, &candidate)
			if err := validateCandidateIdentity(image, active, candidate, entity.OutputModeHTML); err == nil {
				t.Fatal("mismatched candidate contract was accepted")
			}
		})
	}
}

func TestValidateContainerRequiresUnloadedRuntime(t *testing.T) {
	tests := []api.ModelRuntime{
		{State: api.RuntimeReady, Loaded: true},
		{State: api.RuntimeBusy, Loaded: true, ActiveRequests: 1},
	}
	for _, runtime := range tests {
		t.Run(string(runtime.State), func(t *testing.T) {
			dockerFake := &validationDocker{info: docker.GetContainerInfoResult{
				ID: testCandidateID, ImageID: testImageID, Name: "/candidate-model", Lifecycle: docker.LifecycleRunning,
			}}
			apiFake := &validationAPI{
				modelInfo: api.GetModelInfoResponse{Success: true, Data: validCandidateModelInfo()},
				runtime:   runtime,
			}
			validator := newTestValidator(t, dockerFake, apiFake)

			_, err := validator.ValidateContainer(context.Background(), ValidateCandidateContainer{
				ContainerID:          testCandidateID,
				Image:                validResolvedImage(),
				ActiveModelInfo:      api.ModelInfo{ModelID: "model-1", Version: "1.0.0"},
				RegisteredOutputMode: entity.OutputModeHTML,
			})
			if !errors.Is(err, ErrCandidateContract) {
				t.Fatalf("expected candidate contract error, got %v", err)
			}
		})
	}
}

func TestValidateContainerBoundsHTTPReadiness(t *testing.T) {
	dockerFake := &validationDocker{info: docker.GetContainerInfoResult{
		ID: testCandidateID, ImageID: testImageID, Name: "/candidate-model", Lifecycle: docker.LifecycleRunning,
	}}
	apiFake := &validationAPI{modelErr: errors.New("not ready")}
	validator, err := NewCandidateValidator(dockerFake, apiFake, CandidateValidationConfig{
		AllowedImageNamespace: "heartwisehub",
		ReadinessTimeout:      10 * time.Millisecond,
		PollInterval:          time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = validator.ValidateContainer(context.Background(), ValidateCandidateContainer{
		ContainerID:          testCandidateID,
		Image:                validResolvedImage(),
		ActiveModelInfo:      api.ModelInfo{ModelID: "model-1", Version: "1.0.0"},
		RegisteredOutputMode: entity.OutputModeHTML,
	})
	if !errors.Is(err, ErrCandidateReadiness) {
		t.Fatalf("expected readiness error, got %v", err)
	}
}

func TestNewerSemanticVersionFollowsPrereleasePrecedence(t *testing.T) {
	tests := []struct {
		active    string
		candidate string
		newer     bool
	}{
		{active: "1.9.9", candidate: "2.0.0", newer: true},
		{active: "2.0.0-rc.2", candidate: "2.0.0-rc.10", newer: true},
		{active: "2.0.0-rc.99999999999999999998", candidate: "2.0.0-rc.99999999999999999999", newer: true},
		{active: "2.0.0-rc.10", candidate: "2.0.0", newer: true},
		{active: "2.0.0", candidate: "2.0.0-rc.10", newer: false},
		{active: "2.0.0", candidate: "2.0.0", newer: false},
		{active: "not-semver", candidate: "2.0.0", newer: false},
	}
	for _, test := range tests {
		if result := newerSemanticVersion(test.active, test.candidate); result != test.newer {
			t.Fatalf("newerSemanticVersion(%q, %q)=%v want %v", test.active, test.candidate, result, test.newer)
		}
	}
}

func newTestValidator(t *testing.T, dockerFake CandidateDocker, apiFake CandidateAPI) *CandidateValidator {
	t.Helper()
	validator, err := NewCandidateValidator(dockerFake, apiFake, CandidateValidationConfig{
		AllowedImageNamespace: "heartwisehub",
		ReadinessTimeout:      100 * time.Millisecond,
		PollInterval:          time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func validInspection() docker.InspectImageResult {
	return docker.InspectImageResult{
		ID:          testImageID,
		RepoDigests: []string{"heartwisehub/other@" + testDigest, "heartwisehub/model@" + testDigest},
		Labels:      validLabels(),
	}
}

func validResolvedImage() ResolvedCandidateImage {
	return ResolvedCandidateImage{
		Reference:    "heartwisehub/model:2.0.0",
		Repository:   "docker.io/heartwisehub/model",
		Tag:          "2.0.0",
		Digest:       testDigest,
		LocalImageID: testImageID,
		labels:       validLabels(),
	}
}

func validCandidateModelInfo() api.ModelInfo {
	sourceRevision := testSourceSHA
	return api.ModelInfo{
		ModelID:              "model-1",
		ModelName:            "Test model",
		Version:              "2.0.0",
		SupportedOutputModes: []string{"JSON", "HTML"},
		Resources: api.ModelResources{
			MaxConcurrentInferences: 1,
			IdleTimeoutSeconds:      60,
		},
		Provenance: &api.ModelProvenance{
			SourceRepository: "HeartWise-AI/pacs-ai-backend",
			SourceRevision:   &sourceRevision,
			ModelRepository:  "HeartWise-AI/model",
			ModelRevision:    testModelSHA,
			WeightsPath:      "weights/model.pt",
			WeightsSHA256:    testWeightsSHA,
		},
	}
}

func validLabels() map[string]string {
	return map[string]string{
		"org.opencontainers.image.version":  "2.0.0",
		"org.opencontainers.image.revision": testSourceSHA,
		"org.opencontainers.image.source":   "https://github.com/HeartWise-AI/pacs-ai-backend",
		"ai.heartwise.model.repository":     "HeartWise-AI/model",
		"ai.heartwise.model.revision":       testModelSHA,
		"ai.heartwise.model.weights.path":   "weights/model.pt",
		"ai.heartwise.model.weights.sha256": testWeightsSHA,
	}
}
