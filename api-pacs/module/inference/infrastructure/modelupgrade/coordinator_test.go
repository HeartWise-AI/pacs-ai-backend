package modelupgrade

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

const (
	testActiveContainerID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testOldImageReference = "heartwisehub/model:1.0.0"
)

type coordinatorDocker struct {
	events       *[]string
	created      docker.CreateCandidateContainer
	createErr    error
	removeErr    error
	removed      []string
	activeInfo   docker.GetContainerInfoResult
	candidateID  string
	pullStarted  chan struct{}
	continuePull chan struct{}
}

func (fake *coordinatorDocker) PullImage(context.Context, string) error {
	fake.record("docker.pull")
	if fake.pullStarted != nil {
		close(fake.pullStarted)
	}
	if fake.continuePull != nil {
		<-fake.continuePull
	}
	return nil
}

func (fake *coordinatorDocker) InspectImage(context.Context, string) (docker.InspectImageResult, error) {
	return docker.InspectImageResult{}, nil
}

func (fake *coordinatorDocker) CreateCandidateContainer(
	_ context.Context,
	request docker.CreateCandidateContainer,
) (string, error) {
	fake.record("docker.create")
	fake.created = request
	return fake.candidateID, fake.createErr
}

func (fake *coordinatorDocker) GetContainerInfo(context.Context, string) (docker.GetContainerInfoResult, error) {
	fake.record("docker.active")
	return fake.activeInfo, nil
}

func (fake *coordinatorDocker) StartContainer(context.Context, string) error { return nil }
func (fake *coordinatorDocker) StopContainer(context.Context, string) error  { return nil }

func (fake *coordinatorDocker) RemoveContainer(_ context.Context, id string) error {
	fake.record("docker.remove:" + id)
	fake.removed = append(fake.removed, id)
	return fake.removeErr
}

func (fake *coordinatorDocker) record(event string) { *fake.events = append(*fake.events, event) }

type coordinatorAPI struct {
	events     *[]string
	modelInfo  api.GetModelInfoResponse
	runtime    api.ModelRuntime
	runtimeErr error
}

func (fake *coordinatorAPI) GetModelInfo(context.Context, string) (api.GetModelInfoResponse, error) {
	*fake.events = append(*fake.events, "api.active-model-info")
	return fake.modelInfo, nil
}

func (fake *coordinatorAPI) GetModelRuntime(context.Context, string) (api.ModelRuntime, error) {
	*fake.events = append(*fake.events, "api.candidate-runtime")
	return fake.runtime, fake.runtimeErr
}

type coordinatorValidator struct {
	events      *[]string
	validateErr error
	modelInfo   api.ModelInfo
	resolved    ResolvedCandidateImage
}

func (fake *coordinatorValidator) ValidateImageReference(reference, _ string) (string, error) {
	*fake.events = append(*fake.events, "validator.reference")
	return reference, nil
}

func (fake *coordinatorValidator) ResolveImage(context.Context, string, string) (ResolvedCandidateImage, error) {
	*fake.events = append(*fake.events, "validator.resolve")
	if fake.resolved.Reference != "" {
		return fake.resolved, nil
	}
	return validResolvedImage(), nil
}

func (fake *coordinatorValidator) ValidateContainer(
	_ context.Context,
	request ValidateCandidateContainer,
) (ValidatedCandidate, error) {
	*fake.events = append(*fake.events, "validator.container")
	if fake.validateErr != nil {
		return ValidatedCandidate{}, fake.validateErr
	}
	modelInfo := fake.modelInfo
	if modelInfo.ModelID == "" {
		modelInfo = validCandidateModelInfo()
	}
	return ValidatedCandidate{
		ContainerID:    request.ContainerID,
		ContainerName:  "candidate-model",
		ImageReference: request.Image.Reference,
		ImageDigest:    request.Image.Digest,
		LocalImageID:   request.Image.LocalImageID,
		ModelInfo:      modelInfo,
		Runtime:        api.ModelRuntime{State: api.RuntimeUnloaded},
	}, nil
}

type coordinatorManager struct {
	events *[]string
	fail   map[string]error
}

func (fake *coordinatorManager) BlockAndDrain(_ context.Context, id string) error {
	return fake.operation("drain:" + id)
}

func (fake *coordinatorManager) ReplaceDrained(oldID, newID string) error {
	return fake.operation("replace:" + oldID + ":" + newID)
}

func (fake *coordinatorManager) PrepareBlocked(_ context.Context, id string) error {
	return fake.operation("prepare:" + id)
}

func (fake *coordinatorManager) Unblock(id string) error {
	return fake.operation("unblock:" + id)
}

func (fake *coordinatorManager) operation(event string) error {
	*fake.events = append(*fake.events, "manager."+event)
	return fake.fail[event]
}

type coordinatorModels struct {
	events *[]string
	model  entity.InferenceModel
}

type coordinatorVersions struct {
	events  *[]string
	version entity.InferenceModelVersion
	err     error
}

func (fake *coordinatorVersions) SelectInferenceModelVersion(
	context.Context,
	string,
) (entity.InferenceModelVersion, error) {
	*fake.events = append(*fake.events, "versions.select")
	return fake.version, fake.err
}

func (fake *coordinatorModels) SelectInferenceModelByID(context.Context, string) (entity.InferenceModel, error) {
	*fake.events = append(*fake.events, "models.select")
	return fake.model, nil
}

type coordinatorRegistrations struct {
	events     *[]string
	previous   entity.InferenceModelDeployment
	replaceErr error
	restoreErr error
	replaced   repositoryTypes.ReplaceInferenceModelRegistration
	restored   repositoryTypes.RestoreInferenceModelRegistration
}

func (fake *coordinatorRegistrations) ReplaceInferenceModelRegistration(
	_ context.Context,
	request repositoryTypes.ReplaceInferenceModelRegistration,
) (entity.InferenceModelDeployment, error) {
	*fake.events = append(*fake.events, "registration.replace")
	fake.replaced = request
	return fake.previous, fake.replaceErr
}

func (fake *coordinatorRegistrations) RestoreInferenceModelRegistration(
	_ context.Context,
	request repositoryTypes.RestoreInferenceModelRegistration,
) error {
	*fake.events = append(*fake.events, "registration.restore")
	fake.restored = request
	return fake.restoreErr
}

type coordinatorFixture struct {
	coordinator   *UpgradeCoordinator
	docker        *coordinatorDocker
	api           *coordinatorAPI
	validator     *coordinatorValidator
	manager       *coordinatorManager
	versions      *coordinatorVersions
	registrations *coordinatorRegistrations
	events        *[]string
}

func newCoordinatorFixture(t *testing.T) coordinatorFixture {
	t.Helper()
	events := []string{}
	dockerFake := &coordinatorDocker{
		events:      &events,
		candidateID: testCandidateID,
		activeInfo: docker.GetContainerInfoResult{
			ID: testActiveContainerID, Name: "/active-model", ImageID: "sha256:old",
		},
	}
	apiFake := &coordinatorAPI{
		events:    &events,
		modelInfo: api.GetModelInfoResponse{Success: true, Data: api.ModelInfo{ModelID: "model-1", Version: "1.0.0"}},
		runtime:   api.ModelRuntime{State: api.RuntimeReady, Loaded: true},
	}
	validatorFake := &coordinatorValidator{events: &events}
	managerFake := &coordinatorManager{events: &events, fail: map[string]error{}}
	modelsFake := &coordinatorModels{events: &events, model: entity.InferenceModel{
		ID:          "registration-1",
		TenantID:    "tenant-1",
		ContainerID: testActiveContainerID,
		DockerImage: testOldImageReference,
		Envs:        []string{"TOKEN=secret"},
		OutputMode:  entity.OutputModeHTML,
	}}
	versionsFake := &coordinatorVersions{events: &events, version: validCoordinatorVersion()}
	registrationsFake := &coordinatorRegistrations{
		events: &events,
		previous: entity.InferenceModelDeployment{
			ContainerID: testActiveContainerID, ImageReference: testOldImageReference,
		},
	}
	coordinator, err := NewUpgradeCoordinator(
		dockerFake,
		apiFake,
		validatorFake,
		managerFake,
		modelsFake,
		versionsFake,
		registrationsFake,
		CoordinatorConfig{DrainTimeout: time.Second, RollbackTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return coordinatorFixture{
		coordinator: coordinator, docker: dockerFake, api: apiFake, validator: validatorFake,
		manager: managerFake, versions: versionsFake, registrations: registrationsFake, events: &events,
	}
}

func (fixture coordinatorFixture) upgrade(t *testing.T) (UpgradeResult, error) {
	t.Helper()
	return fixture.coordinator.Upgrade(context.Background(), UpgradeRequest{
		TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version-2",
	})
}

func validCoordinatorVersion() entity.InferenceModelVersion {
	modelInfo := validCandidateModelInfo()
	provenance := modelInfo.Provenance
	return entity.InferenceModelVersion{
		ID:             "catalog-version-2",
		ModelID:        modelInfo.ModelID,
		Version:        modelInfo.Version,
		ImageReference: "heartwisehub/model:2.0.0",
		ImageDigest:    testDigest,
		Status:         entity.InferenceModelVersionAvailable,
		Provenance: &entity.InferenceModelProvenance{
			SourceRepository: provenance.SourceRepository,
			SourceRevision:   provenance.SourceRevision,
			ModelRepository:  provenance.ModelRepository,
			ModelRevision:    provenance.ModelRevision,
			WeightsPath:      provenance.WeightsPath,
			WeightsSHA256:    provenance.WeightsSHA256,
		},
	}
}

func TestUpgradeCoordinatorActivatesValidatedCandidate(t *testing.T) {
	fixture := newCoordinatorFixture(t)

	result, err := fixture.upgrade(t)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"models.select", "docker.active", "api.active-model-info", "versions.select", "validator.reference",
		"docker.pull", "validator.resolve", "docker.create", "validator.container",
		"manager.drain:" + testActiveContainerID,
		"manager.replace:" + testActiveContainerID + ":" + testCandidateID,
		"manager.prepare:" + testCandidateID, "api.candidate-runtime", "registration.replace",
		"manager.unblock:" + testCandidateID,
	}
	if !reflect.DeepEqual(*fixture.events, wantEvents) {
		t.Fatalf("events:\n got: %v\nwant: %v", *fixture.events, wantEvents)
	}
	if fixture.docker.created.Image != testImageID {
		t.Fatalf("candidate was created from %q instead of immutable local image %q", fixture.docker.created.Image, testImageID)
	}
	if fixture.docker.created.ActiveContainerID != testActiveContainerID ||
		!reflect.DeepEqual(fixture.docker.created.Envs, []string{"TOKEN=secret"}) {
		t.Fatalf("candidate did not inherit active runtime settings: %+v", fixture.docker.created)
	}
	if len(fixture.docker.removed) != 0 {
		t.Fatalf("successful upgrade removed rollback baseline: %v", fixture.docker.removed)
	}
	if result.Active.ContainerID != testCandidateID || result.Previous.ContainerID != testActiveContainerID {
		t.Fatalf("unexpected upgrade result: %+v", result)
	}
}

func TestUpgradeCoordinatorReportsEveryStageInOrder(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	stages := make([]UpgradeStage, 0, 8)
	_, err := fixture.coordinator.Upgrade(context.Background(), UpgradeRequest{
		TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version-2",
		OnStage: func(_ context.Context, stage UpgradeStage) error {
			stages = append(stages, stage)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []UpgradeStage{
		UpgradeStageResolveActive, UpgradeStageResolveVersion, UpgradeStagePull, UpgradeStageCreate,
		UpgradeStageValidate, UpgradeStageDrain, UpgradeStagePrepare, UpgradeStageActivate,
	}
	if !reflect.DeepEqual(stages, want) {
		t.Fatalf("stages = %v want %v", stages, want)
	}
}

func TestUpgradeCoordinatorCleansCandidateWhenStagePersistenceFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	persistErr := errors.New("persist stage")
	_, err := fixture.coordinator.Upgrade(context.Background(), UpgradeRequest{
		TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version-2",
		OnStage: func(_ context.Context, stage UpgradeStage) error {
			if stage == UpgradeStageValidate {
				return persistErr
			}
			return nil
		},
	})
	upgradeErr := assertUpgradeStage(t, err, UpgradeStageValidate)
	if !errors.Is(upgradeErr.Err, persistErr) || !reflect.DeepEqual(fixture.docker.removed, []string{testCandidateID}) {
		t.Fatalf("upgrade error=%v removed=%v", upgradeErr, fixture.docker.removed)
	}
	if slices.Contains(*fixture.events, "manager.drain:"+testActiveContainerID) {
		t.Fatal("active model was drained after progress persistence failed")
	}
}

func TestUpgradeCoordinatorRejectsUnavailableOrMismatchedCatalogVersionBeforePull(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*entity.InferenceModelVersion)
	}{
		{name: "revoked", change: func(version *entity.InferenceModelVersion) {
			version.Status = entity.InferenceModelVersionRevoked
		}},
		{name: "wrong model", change: func(version *entity.InferenceModelVersion) {
			version.ModelID = "other-model"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoordinatorFixture(t)
			test.change(&fixture.versions.version)
			_, err := fixture.upgrade(t)
			assertUpgradeStage(t, err, UpgradeStageResolveVersion)
			assertEventAbsent(t, *fixture.events, "docker.pull")
		})
	}
}

func TestUpgradeCoordinatorRejectsCandidateWhenEnvironmentDisallowsIt(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	version := &fixture.versions.version
	version.Channel = entity.InferenceModelVersionCandidate
	version.BaseVersion = "2.0.0"
	version.Version = "2.0.0-rc.366.abcdef123456"
	version.ImageReference = "heartwisehub/model:" + version.Version
	version.SourceBranch = "issue-365-transactional-model-upgrades"
	version.PullRequest = 366
	version.WorkflowRun = "123456789"

	_, err := fixture.upgrade(t)
	upgradeErr := assertUpgradeStage(t, err, UpgradeStageResolveVersion)
	if upgradeErr.Err.Error() != apiError.InferenceCandidateDeploymentDisabled {
		t.Fatalf("candidate deployment returned %v", upgradeErr)
	}
	assertEventAbsent(t, *fixture.events, "docker.pull")
}

func TestUpgradeCoordinatorAllowsCandidateInConfiguredEnvironment(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	candidateInfo := validCandidateModelInfo()
	candidateInfo.Version = "2.0.0-rc.366.abcdef123456"
	fixture.validator.modelInfo = candidateInfo
	fixture.validator.resolved = validResolvedImage()
	fixture.validator.resolved.Reference = "heartwisehub/model:" + candidateInfo.Version
	fixture.validator.resolved.Tag = candidateInfo.Version
	fixture.coordinator.config.AllowCandidateDeployments = true
	version := &fixture.versions.version
	version.Channel = entity.InferenceModelVersionCandidate
	version.BaseVersion = "2.0.0"
	version.Version = candidateInfo.Version
	version.ImageReference = "heartwisehub/model:" + version.Version
	version.SourceBranch = "issue-365-transactional-model-upgrades"
	version.PullRequest = 366
	version.WorkflowRun = "123456789"

	result, err := fixture.upgrade(t)
	if err != nil {
		t.Fatal(err)
	}
	if result.Active.CatalogVersionID != version.ID {
		t.Fatalf("candidate deployment result = %+v", result)
	}
}

func TestUpgradeCoordinatorDoesNotDrainWhenCandidateValidationFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.validator.validateErr = errors.New("candidate rejected")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStageValidate)
	assertEventAbsent(t, *fixture.events, "manager.")
	assertEventsEndWith(t, *fixture.events, "validator.container", "docker.remove:"+testCandidateID)
}

func TestUpgradeCoordinatorRollsBackManagerWhenCandidatePreparationFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.manager.fail["prepare:"+testCandidateID] = errors.New("load failed")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStagePrepare)
	assertEventsEndWith(t, *fixture.events,
		"manager.prepare:"+testCandidateID,
		"manager.drain:"+testCandidateID,
		"manager.replace:"+testCandidateID+":"+testActiveContainerID,
		"manager.prepare:"+testActiveContainerID,
		"manager.unblock:"+testActiveContainerID,
		"docker.remove:"+testCandidateID,
	)
}

func TestUpgradeCoordinatorRollsBackManagerWhenRegistrationSwitchFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.registrations.replaceErr = errors.New("registration conflict")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStageActivate)
	assertEventAbsent(t, *fixture.events, "registration.restore")
	assertEventsEndWith(t, *fixture.events,
		"registration.replace",
		"manager.drain:"+testCandidateID,
		"manager.replace:"+testCandidateID+":"+testActiveContainerID,
		"manager.prepare:"+testActiveContainerID,
		"manager.unblock:"+testActiveContainerID,
		"docker.remove:"+testCandidateID,
	)
}

func TestUpgradeCoordinatorRestoresRegistrationWhenCandidateCannotBeUnblocked(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.manager.fail["unblock:"+testCandidateID] = errors.New("candidate unsafe")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStageActivate)
	assertEventsEndWith(t, *fixture.events,
		"registration.replace",
		"manager.unblock:"+testCandidateID,
		"manager.drain:"+testCandidateID,
		"manager.replace:"+testCandidateID+":"+testActiveContainerID,
		"manager.prepare:"+testActiveContainerID,
		"registration.restore",
		"manager.unblock:"+testActiveContainerID,
		"docker.remove:"+testCandidateID,
	)
	if fixture.registrations.restored.Previous.ContainerID != testActiveContainerID ||
		fixture.registrations.restored.ExpectedContainerID != testCandidateID {
		t.Fatalf("incorrect registration rollback: %+v", fixture.registrations.restored)
	}
}

func TestUpgradeCoordinatorRetainsCandidateWhenRollbackCannotBeConfirmed(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	rollbackErr := errors.New("candidate drain failed")
	fixture.manager.fail["prepare:"+testCandidateID] = errors.New("load failed")
	fixture.manager.fail["drain:"+testCandidateID] = rollbackErr

	_, err := fixture.upgrade(t)
	upgradeErr := assertUpgradeStage(t, err, UpgradeStagePrepare)
	if !errors.Is(upgradeErr.RollbackErr, rollbackErr) {
		t.Fatalf("rollback error was not preserved: %v", upgradeErr.RollbackErr)
	}
	assertEventAbsent(t, *fixture.events, "docker.remove:")
}

func TestUpgradeCoordinatorRejectsConcurrentUpgradeForSameModel(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.docker.pullStarted = make(chan struct{})
	fixture.docker.continuePull = make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := fixture.upgrade(t)
		firstDone <- err
	}()

	<-fixture.docker.pullStarted
	_, err := fixture.upgrade(t)
	upgradeErr := assertUpgradeStage(t, err, UpgradeStageResolveActive)
	if upgradeErr.Err.Error() != "INFERENCE_UPGRADE_CONFLICT" {
		t.Fatalf("concurrent upgrade returned %v", upgradeErr)
	}

	close(fixture.docker.continuePull)
	if err = <-firstDone; err != nil {
		t.Fatalf("first upgrade failed: %v", err)
	}
}

func assertUpgradeStage(t *testing.T, err error, stage UpgradeStage) *UpgradeError {
	t.Helper()
	var upgradeErr *UpgradeError
	if !errors.As(err, &upgradeErr) || upgradeErr.Stage != stage {
		t.Fatalf("upgrade error = %v, want stage %s", err, stage)
	}
	return upgradeErr
}

func assertEventAbsent(t *testing.T, events []string, prefix string) {
	t.Helper()
	for _, event := range events {
		if len(event) >= len(prefix) && event[:len(prefix)] == prefix {
			t.Fatalf("unexpected event %q in %v", event, events)
		}
	}
}

func assertEventsEndWith(t *testing.T, events []string, suffix ...string) {
	t.Helper()
	if len(events) < len(suffix) || !reflect.DeepEqual(events[len(events)-len(suffix):], suffix) {
		t.Fatalf("events %v do not end with %v", events, suffix)
	}
}
