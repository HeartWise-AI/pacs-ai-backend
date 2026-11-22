package modelupgrade

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/modelmanager"
)

const (
	testLocalImageID  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testLocalImageRef = "model-local:1.0.1-render-report-1-5677f4f8"
)

// moveFixture is a registration whose container was replaced by hand with a
// locally built image, while its catalog version is still the published one.
func moveFixture(t *testing.T) coordinatorFixture {
	t.Helper()
	f := newCoordinatorFixture(t)
	f.docker.activeInfo = docker.GetContainerInfoResult{
		ID: testActiveContainerID, Name: "/active-model", ImageID: testLocalImageID, ImageReference: testLocalImageRef,
	}
	local := docker.InspectImageResult{ID: testLocalImageID, RepoTags: []string{testLocalImageRef}}
	f.docker.images = map[string]docker.InspectImageResult{testLocalImageID: local, testLocalImageRef: local}
	f.docker.infos = map[string]docker.GetContainerInfoResult{
		testCandidateID: {ID: testCandidateID, Name: "/active-model-candidate", ImageID: testLocalImageID, GPUUUID: explicitTestGPU},
	}
	running := validCandidateModelInfo()
	running.Version = "1.0.1"
	f.api.modelInfo = api.GetModelInfoResponse{Success: true, Data: running}
	f.validator.modelInfo = running
	f.validator.gpuUUID = explicitTestGPU
	sourceRevision := testSourceSHA
	model := &f.coordinator.models.(*coordinatorModels).model
	model.DockerImage = testLocalImageRef
	model.Deployment = &entity.InferenceModelDeployment{
		ContainerID: testActiveContainerID, CatalogVersionID: "catalog-version-2", ArtifactModelID: "model-1",
		ImageReference: testLocalImageRef, LocalImageID: testLocalImageID, ModelVersion: "1.0.1",
		Provenance: &entity.InferenceModelProvenance{
			SourceRepository: "HeartWise-AI/pacs-ai-backend", SourceRevision: &sourceRevision,
			ModelRepository: "HeartWise-AI/model", ModelRevision: testModelSHA,
			WeightsPath: "weights/model.pt", WeightsSHA256: testWeightsSHA,
		},
		DeployedAt: 1,
	}
	f.coordinator.manager = &placementCoordinatorManager{coordinatorManager: f.manager}
	return f
}

func (fixture coordinatorFixture) move(automatic bool) (UpgradeResult, error) {
	return fixture.coordinator.Upgrade(context.Background(), UpgradeRequest{
		TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version-2",
		TargetGPUUUID: explicitTestGPU, ExpectedContainerID: testActiveContainerID,
		KeepRunningImage: true, Automatic: automatic,
	})
}

func TestMoveRecreatesTheContainerFromTheImageItRuns(t *testing.T) {
	f := moveFixture(t)
	result, err := f.move(false)
	if err != nil {
		t.Fatal(err)
	}
	created := f.docker.created
	if created.Image != testLocalImageRef || !created.KeepActiveConfiguration || created.ExpectedImageID != testLocalImageID {
		t.Fatalf("candidate was not recreated from the running image: %+v", created)
	}
	if created.Envs != nil {
		t.Fatalf("a move must keep the container's own environment, got the registration's: %v", created.Envs)
	}
	if created.GPUUUID != explicitTestGPU || created.ActiveContainerID != testActiveContainerID {
		t.Fatalf("unexpected move target: %+v", created)
	}
	for _, event := range *f.events {
		if event == "docker.pull" || event == "validator.resolve" || event == "validator.reference" {
			t.Fatalf("a move must not pull or resolve the registered image: %v", *f.events)
		}
	}
	if result.ImageSource != ImageSourceRunning || result.Image == nil || !result.Image.Drift {
		t.Fatalf("image drift was not reported: %+v", result)
	}
	if result.Image.RegisteredReference != "heartwisehub/model:2.0.0" || result.Image.Reference != testLocalImageRef {
		t.Fatalf("unexpected image report: %+v", result.Image)
	}
	replaced := f.registrations.replaced
	if !replaced.KeepsRunningImage || replaced.Deployment.LocalImageID != testLocalImageID ||
		replaced.Deployment.ImageReference != testLocalImageRef || replaced.Deployment.ImageDigest != "" ||
		replaced.Deployment.CatalogVersionID != "catalog-version-2" || replaced.Deployment.ModelVersion != "1.0.1" ||
		replaced.Deployment.GPUUUID != explicitTestGPU || replaced.Deployment.ContainerID != testCandidateID {
		t.Fatalf("registration does not describe the moved container: %+v", replaced)
	}
	if !slices.Contains(*f.events, "manager.drain:"+testActiveContainerID) || !slices.Contains(*f.events, "manager.unblock:"+testCandidateID) {
		t.Fatalf("move did not go through drain and activation: %v", *f.events)
	}
}

func TestMoveOfTheRegisteredImageReportsNoDrift(t *testing.T) {
	f := moveFixture(t)
	official := docker.InspectImageResult{
		ID: testImageID, RepoTags: []string{"heartwisehub/model:2.0.0"}, RepoDigests: []string{"heartwisehub/model@" + testDigest},
	}
	f.docker.activeInfo.ImageID, f.docker.activeInfo.ImageReference = testImageID, testImageID // created by the gateway from an ID
	f.docker.images = map[string]docker.InspectImageResult{testImageID: official, "heartwisehub/model:2.0.0": official}
	f.docker.infos[testCandidateID] = docker.GetContainerInfoResult{ID: testCandidateID, ImageID: testImageID, GPUUUID: explicitTestGPU}
	model := &f.coordinator.models.(*coordinatorModels).model
	model.Deployment.LocalImageID, model.Deployment.ImageReference, model.Deployment.ImageDigest = testImageID, "heartwisehub/model:2.0.0", testDigest
	model.DockerImage = "heartwisehub/model:2.0.0"

	result, err := f.move(false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Image == nil || result.Image.Drift || result.Image.Digest != testDigest {
		t.Fatalf("the registered image must not be reported as drift: %+v", result.Image)
	}
	if f.docker.created.Image != "heartwisehub/model:2.0.0" || f.registrations.replaced.Deployment.ImageDigest != testDigest {
		t.Fatalf("unexpected create=%+v registration=%+v", f.docker.created, f.registrations.replaced.Deployment)
	}
}

func TestMoveUsesTheImageIDWhenItsTagWasMovedToAnotherImage(t *testing.T) {
	f := moveFixture(t)
	f.docker.images[testLocalImageRef] = docker.InspectImageResult{ID: testImageID} // the tag now names a newer build
	if _, err := f.move(false); err != nil {
		t.Fatal(err)
	}
	if f.docker.created.Image != testLocalImageID {
		t.Fatalf("candidate must be created from the immutable running image, got %q", f.docker.created.Image)
	}
}

func TestMoveFallsBackToTheRegisteredImageOnlyWhenTheRunningImageIsGone(t *testing.T) {
	f := moveFixture(t)
	f.docker.images = map[string]docker.InspectImageResult{} // the running image was deleted from the host
	f.validator.modelInfo = validCandidateModelInfo()        // the catalog image reports the catalog version
	f.api.modelInfo.Data.Version = "2.0.0"
	result, err := f.move(false)
	if err != nil {
		t.Fatal(err)
	}
	if result.ImageSource != ImageSourceRegistered || result.Image != nil {
		t.Fatalf("fallback was not reported: %+v", result)
	}
	if !slices.Contains(*f.events, "docker.pull") || f.docker.created.KeepActiveConfiguration || f.docker.created.Image != testImageID {
		t.Fatalf("fallback did not deploy the registered image as before: events=%v created=%+v", *f.events, f.docker.created)
	}
	if f.registrations.replaced.KeepsRunningImage {
		t.Fatal("a catalog deployment must keep the strict registration contract")
	}
}

func TestAutomaticMoveIsRefusedWhenItCouldNotKeepTheRunningImage(t *testing.T) {
	f := moveFixture(t)
	f.docker.images = map[string]docker.InspectImageResult{}
	_, err := f.move(true)
	if !errors.Is(err, ErrMoveWouldChangeImage) {
		t.Fatalf("expected the move to be refused, got %v", err)
	}
	if f.docker.created.Name != "" || slices.Contains(*f.events, "docker.pull") {
		t.Fatalf("a refused automatic move touched Docker: %v", *f.events)
	}
}

func TestMoveIsRefusedWhenTheCreatedContainerRunsAnotherImage(t *testing.T) {
	f := moveFixture(t)
	f.docker.infos[testCandidateID] = docker.GetContainerInfoResult{ID: testCandidateID, ImageID: testImageID}
	_, err := f.move(false)
	if !errors.Is(err, ErrMoveWouldChangeImage) {
		t.Fatalf("expected the image assertion to fail, got %v", err)
	}
	if !slices.Contains(f.docker.removed, testCandidateID) {
		t.Fatal("the mismatching candidate was not removed")
	}
	for _, event := range *f.events {
		if strings.HasPrefix(event, "manager.") || event == "registration.replace" {
			t.Fatalf("the active model was disturbed: %v", *f.events)
		}
	}
}

type idleCoordinatorManager struct {
	*placementCoordinatorManager
	busy bool
}

func (manager *idleCoordinatorManager) BlockAndDrainIdle(_ context.Context, id string) error {
	if manager.busy {
		*manager.events = append(*manager.events, "manager.drain-idle-refused:"+id)
		return modelmanager.ErrModelBusy
	}
	return manager.operation("drain-idle:" + id)
}

func TestAutomaticMoveNeverDrainsAModelThatIsInUse(t *testing.T) {
	for _, busy := range []bool{false, true} {
		f := moveFixture(t)
		manager := &idleCoordinatorManager{placementCoordinatorManager: f.coordinator.manager.(*placementCoordinatorManager), busy: busy}
		f.coordinator.manager = manager
		_, err := f.move(true)
		if slices.Contains(*f.events, "manager.drain:"+testActiveContainerID) {
			t.Fatalf("an automatic move used the waiting drain: %v", *f.events)
		}
		if !busy {
			if err != nil || !slices.Contains(*f.events, "manager.drain-idle:"+testActiveContainerID) {
				t.Fatalf("idle model was not moved: err=%v events=%v", err, *f.events)
			}
			continue
		}
		if !errors.Is(err, ErrModelBusy) {
			t.Fatalf("expected a busy refusal, got %v", err)
		}
		if !slices.Contains(f.docker.removed, testCandidateID) {
			t.Fatal("candidate of a refused move was not removed")
		}
		for _, event := range *f.events {
			if strings.HasPrefix(event, "manager.unblock") || strings.HasPrefix(event, "manager.replace") || event == "registration.replace" {
				t.Fatalf("a refused move changed the active model: %v", *f.events)
			}
		}
	}
}

func TestAutomaticMoveNeedsAManagerThatCanRefuseABusyModel(t *testing.T) {
	f := moveFixture(t)
	if _, err := f.move(true); err == nil || slices.Contains(*f.events, "manager.drain:"+testActiveContainerID) {
		t.Fatalf("automatic move drained without the idle check: err=%v events=%v", err, *f.events)
	}
}

func TestResolveRunningImageComparesWithTheCatalogVersion(t *testing.T) {
	version := validCoordinatorVersion()
	registered := entity.InferenceModel{DockerImage: "heartwisehub/model:2.0.0", Deployment: &entity.InferenceModelDeployment{
		ImageReference: "heartwisehub/model:2.0.0", ImageDigest: testDigest, LocalImageID: testImageID,
	}}
	official := docker.InspectImageResult{ID: testImageID, RepoTags: []string{"heartwisehub/model:2.0.0"}, RepoDigests: []string{"heartwisehub/model@" + testDigest}}
	local := docker.InspectImageResult{ID: testLocalImageID, RepoTags: []string{testLocalImageRef}}
	inspector := &coordinatorDocker{images: map[string]docker.InspectImageResult{
		testImageID: official, "heartwisehub/model:2.0.0": official, testLocalImageID: local, testLocalImageRef: local,
	}}

	same := ResolveRunningImage(context.Background(), inspector, docker.GetContainerInfoResult{ImageID: testImageID, ImageReference: "heartwisehub/model:2.0.0"}, registered, &version)
	if !same.Available || same.Drift || same.Digest != testDigest || same.CreateReference != "heartwisehub/model:2.0.0" {
		t.Fatalf("registered image: %+v", same)
	}
	drifted := ResolveRunningImage(context.Background(), inspector, docker.GetContainerInfoResult{ImageID: testLocalImageID, ImageReference: testLocalImageRef}, registered, &version)
	if !drifted.Available || !drifted.Drift || drifted.Reference != testLocalImageRef || drifted.RegisteredReference != "heartwisehub/model:2.0.0" || drifted.Digest != "" {
		t.Fatalf("local build: %+v", drifted)
	}
	// Without a readable catalog entry the registration snapshot is the reference.
	snapshot := ResolveRunningImage(context.Background(), inspector, docker.GetContainerInfoResult{ImageID: testLocalImageID, ImageReference: testLocalImageID}, registered, nil)
	if !snapshot.Drift || snapshot.Reference != testLocalImageRef {
		t.Fatalf("snapshot comparison: %+v", snapshot)
	}
	gone := ResolveRunningImage(context.Background(), inspector, docker.GetContainerInfoResult{ImageID: "sha256:" + strings.Repeat("9", 64)}, registered, &version)
	if gone.Available || gone.Drift {
		t.Fatalf("an image that is not on the host cannot be compared: %+v", gone)
	}
}

func TestValidatorAcceptsAMovedLocalImageOnlyWhenItMatchesTheActiveModel(t *testing.T) {
	active := validCandidateModelInfo()
	active.Version = "1.0.1"
	active.Capabilities = &api.ModelCapabilities{RenderReport: true}
	request := func() ValidateCandidateContainer {
		return ValidateCandidateContainer{
			ContainerID: testCandidateID, ActiveModelInfo: active, RegisteredOutputMode: entity.OutputModeHTML, SameImageAsActive: true,
			// A local build: no registry digest and no release labels.
			Image: ResolvedCandidateImage{Reference: testLocalImageRef, LocalImageID: testImageID},
		}
	}
	dockerFake := &validationDocker{info: docker.GetContainerInfoResult{
		ID: testCandidateID, Name: "/candidate-model", ImageID: testImageID, Lifecycle: docker.LifecycleRunning,
	}}
	apiFake := &validationAPI{modelInfo: api.GetModelInfoResponse{Success: true, Data: active}, runtime: api.ModelRuntime{State: api.RuntimeUnloaded}}
	validated, err := newTestValidator(t, dockerFake, apiFake).ValidateContainer(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if validated.LocalImageID != testImageID || validated.ImageDigest != "" || validated.ModelInfo.Version != "1.0.1" {
		t.Fatalf("unexpected validated move: %+v", validated)
	}

	strict := request()
	strict.SameImageAsActive = false
	if _, err = newTestValidator(t, dockerFake, apiFake).ValidateContainer(context.Background(), strict); !errors.Is(err, ErrCandidateImageIdentity) {
		t.Fatalf("a catalog deployment must still require a registry digest, got %v", err)
	}

	downgraded := active
	downgraded.Capabilities = nil // the published image without the report feature
	apiFake = &validationAPI{modelInfo: api.GetModelInfoResponse{Success: true, Data: downgraded}, runtime: api.ModelRuntime{State: api.RuntimeUnloaded}}
	if _, err = newTestValidator(t, dockerFake, apiFake).ValidateContainer(context.Background(), request()); !errors.Is(err, ErrCandidateContract) {
		t.Fatalf("a moved model that lost a capability must be rejected, got %v", err)
	}
	older := active
	older.Version = "1.0.0"
	apiFake = &validationAPI{modelInfo: api.GetModelInfoResponse{Success: true, Data: older}, runtime: api.ModelRuntime{State: api.RuntimeUnloaded}}
	if _, err = newTestValidator(t, dockerFake, apiFake).ValidateContainer(context.Background(), request()); !errors.Is(err, ErrCandidateContract) {
		t.Fatalf("a moved model reporting another version must be rejected, got %v", err)
	}
}

func TestDeploymentFromMovedCandidateKeepsTheCatalogReference(t *testing.T) {
	f := moveFixture(t)
	registered := f.coordinator.models.(*coordinatorModels).model
	info := validCandidateModelInfo()
	info.Version = "1.0.1"
	deployment, err := DeploymentFromMovedCandidate(
		ValidatedCandidate{ContainerID: testCandidateID, GPUUUID: explicitTestGPU, LocalImageID: testLocalImageID, ModelInfo: info},
		registered, RunningImage{ImageID: testLocalImageID, Reference: testLocalImageRef, Available: true, Drift: true},
		"catalog-version-2", time.Unix(1_700_000_000, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.CatalogVersionID != "catalog-version-2" || deployment.LocalImageID != testLocalImageID || deployment.ImageDigest != "" || deployment.Provenance == nil {
		t.Fatalf("unexpected deployment: %+v", deployment)
	}
	if _, err = DeploymentFromMovedCandidate(
		ValidatedCandidate{ContainerID: testCandidateID, LocalImageID: testImageID, ModelInfo: info},
		registered, RunningImage{ImageID: testLocalImageID, Reference: testLocalImageRef}, "catalog-version-2", time.Unix(1, 0),
	); !errors.Is(err, ErrCandidateContract) {
		t.Fatalf("a candidate of another image must not be recorded as a move, got %v", err)
	}
}
