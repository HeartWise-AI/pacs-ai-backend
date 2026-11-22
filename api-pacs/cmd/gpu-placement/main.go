// gpu-placement recreates existing immutable model artifacts through the normal
// deployment coordinator. Apply requires exclusive maintenance ownership.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/distribution/reference"
	dockerContainer "github.com/docker/docker/api/types/container"
	dockerNetwork "github.com/docker/docker/api/types/network"

	"api-pacs/infrastructures/providers/api/dockerinference"
	"api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/modelmanager"
	"api-pacs/module/inference/infrastructure/modelupgrade"
	"api-pacs/module/inference/infrastructure/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

// Audit must never start a container if it stops between inspection and validation.
type readOnlyDocker struct{ *docker.DockerSDK }

func (d readOnlyDocker) StartContainer(context.Context, string) error {
	return errors.New("audit cannot start a stopped model container")
}

type planEntry struct {
	Registration   entity.InferenceModel        `json:"registration"`
	OriginalName   string                       `json:"originalName"`
	LocalImageID   string                       `json:"localImageId"`
	TargetGPUUUID  string                       `json:"targetGpuUuid"`
	Version        entity.InferenceModelVersion `json:"version"`
	PublishCatalog bool                         `json:"publishCatalog"`
}
type migration struct {
	docker    *docker.DockerSDK
	api       *dockerinference.DockerInferenceAPI
	validator *modelupgrade.CandidateValidator
	models    *repository.InferenceQueryRepository
	versions  *repository.InferenceModelVersionRepository
	commands  *repository.InferenceCommandRepository
	directory string
	actor     string
}

func main() {
	dir := flag.String("journal", "", "Protected directory for plan, original Docker snapshots, and stage journal")
	names := flag.String("containers", "DeepCORO-CTO,DeepCORO-MACE,DeepCORO-SYNTAX,deepcoro-clip-generic,CathEF-CLIP,DeepRV-CLIP,deepcoro-jepa,deepcoro-scad", "Exact registered container names to migrate")
	apply := flag.Bool("apply", false, "Apply after read-only validation; API and study workers must be stopped")
	actor := flag.String("actor", "", "Operator identity recorded in the local migration journal")
	flag.Parse()
	if err := run(*dir, *names, *apply, *actor); err != nil {
		fmt.Fprintln(os.Stderr, "GPU placement failed:", err)
		os.Exit(1)
	}
}

func run(directory, names string, apply bool, actor string) error {
	if directory == "" || (apply && strings.TrimSpace(actor) == "") {
		return errors.New("--journal is required; --apply also requires --actor")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	sdk, err := docker.NewClient(dockerTypes.Config{Username: os.Getenv("DOCKER_USERNAME"), Password: os.Getenv("DOCKER_PASSWORD"), Network: os.Getenv("DOCKER_NETWORK"), GPUDefaultUUID: os.Getenv("MODEL_GPU_DEFAULT_UUID"), GPUAssignmentsJSON: os.Getenv("MODEL_GPU_ASSIGNMENTS")})
	if err != nil {
		return err
	}
	defer sdk.Client.Close()
	if sdk.GPUDefaultUUID == "" {
		return errors.New("explicit MODEL_GPU_DEFAULT_UUID placement is required")
	}
	if apply {
		if err := requireMaintenance(ctx, sdk); err != nil {
			return err
		}
	}
	firebase, err := firebaseadmin.NewApp(ctx, os.Getenv("FIREBASE_CONFIG_FILE_PATH"), os.Getenv("FIREBASE_PROJECT_ID"))
	if err != nil {
		return err
	}
	api := &dockerinference.DockerInferenceAPI{}
	namespace := os.Getenv("MODEL_UPGRADE_ALLOWED_IMAGE_NAMESPACE")
	if namespace == "" {
		namespace = "heartwisehub"
	}
	validator, err := modelupgrade.NewCandidateValidator(readOnlyDocker{sdk}, api, modelupgrade.CandidateValidationConfig{AllowedImageNamespace: namespace, ReadinessTimeout: 2 * time.Minute, PollInterval: 500 * time.Millisecond})
	if err != nil {
		return err
	}
	m := &migration{docker: sdk, api: api, validator: validator, models: &repository.InferenceQueryRepository{FirebaseAdminSDK: firebase}, versions: &repository.InferenceModelVersionRepository{FirebaseAdminSDK: firebase}, commands: &repository.InferenceCommandRepository{FirebaseAdminSDK: firebase}, directory: directory, actor: actor}
	client, err := firebase.App.Firestore(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	docs, err := client.Collection("inference_models").Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(names, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			wanted[name] = false
		}
	}
	if len(wanted) == 0 {
		return errors.New("no container names supplied")
	}
	if apply {
		operationRepository := &repository.InferenceModelDeploymentOperationRepository{FirebaseAdminSDK: firebase}
		pending, err := operationRepository.ListNonTerminalInferenceModelDeploymentOperations(ctx)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			return errors.New("unfinished deployment operations exist; reconcile them before maintenance migration")
		}
	}
	plan := []planEntry{}
	for _, doc := range docs {
		var registered entity.InferenceModel
		if err := doc.DataTo(&registered); err != nil {
			return err
		}
		registered.ID = doc.Ref.ID
		info, err := sdk.GetContainerInfo(ctx, registered.ContainerID)
		if err != nil {
			continue
		}
		name := strings.TrimPrefix(info.Name, "/")
		if _, ok := wanted[name]; !ok {
			continue
		}
		if wanted[name] {
			return fmt.Errorf("container %s is registered more than once", name)
		}
		if info.Lifecycle != dockerTypes.LifecycleRunning {
			return fmt.Errorf("requested model %s must already be running for read-only validation", name)
		}
		entry, err := m.inspect(ctx, registered, name, info.ImageID)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", name, err)
		}
		plan = append(plan, entry)
		wanted[name] = true
	}
	for name, found := range wanted {
		if !found {
			return fmt.Errorf("requested registered model %s was not found", name)
		}
	}
	if err := writeProtected(filepath.Join(directory, "plan.json"), plan); err != nil {
		return err
	}
	if !apply {
		fmt.Printf("Read-only validation passed for %d models; protected plan written. No catalog, registration or container changes made.\n", len(plan))
		return nil
	}
	// Never construct a second manager while the production API owns admission.
	if err := requireMaintenance(ctx, sdk); err != nil {
		return err
	}
	config, err := modelmanager.ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if !config.Enabled || len(config.GPUBudgetsMiB) > 0 {
		return errors.New("apply bootstrap requires enabled manager with scalar budget; enable per-GPU budgets only after migration")
	}
	manager, err := modelmanager.New(config, &modelmanager.ContainerBackend{Docker: sdk, API: api})
	if err != nil {
		return err
	}
	ids, err := m.models.ListRegisteredModelContainerIDs(ctx)
	if err != nil {
		return err
	}
	if err := manager.Reconcile(ctx, ids); err != nil {
		return err
	}
	applyValidator, err := modelupgrade.NewCandidateValidator(sdk, api, modelupgrade.CandidateValidationConfig{AllowedImageNamespace: namespace, ReadinessTimeout: 2 * time.Minute, PollInterval: 500 * time.Millisecond})
	if err != nil {
		return err
	}
	coordinator, err := modelupgrade.NewUpgradeCoordinator(sdk, api, applyValidator, manager, m.models, m.versions, m.commands, modelupgrade.CoordinatorConfig{})
	if err != nil {
		return err
	}
	for _, entry := range plan {
		if err := requireMaintenance(ctx, sdk); err != nil {
			return err
		}
		if err := m.apply(ctx, manager, coordinator, entry); err != nil {
			return fmt.Errorf("migrate %s: %w", entry.OriginalName, err)
		}
	}
	fmt.Printf("Migrated %d existing artifacts; original names retained, models unloaded. Review journal before starting the production manager.\n", len(plan))
	return nil
}

func requireMaintenance(ctx context.Context, sdk *docker.DockerSDK) error {
	containers, err := sdk.Client.ContainerList(ctx, dockerContainer.ListOptions{})
	if err != nil {
		return err
	}
	for _, container := range containers {
		for _, name := range container.Names {
			name = strings.TrimPrefix(name, "/")
			if name == "api-pacs" || name == "study-service" || name == "study-celery-worker" || strings.HasPrefix(name, "study-celery-worker-") {
				return fmt.Errorf("maintenance requires %s stopped; refusing a second admission owner", name)
			}
		}
	}
	return nil
}

func (m *migration) inspect(ctx context.Context, registered entity.InferenceModel, name, imageID string) (planEntry, error) {
	image, err := m.docker.InspectImage(ctx, imageID)
	if err != nil {
		return planEntry{}, err
	}
	requested, err := reference.ParseNormalizedNamed(registered.DockerImage)
	if err != nil {
		return planEntry{}, err
	}
	repositoryName := reference.TrimNamed(requested).Name()
	immutable := ""
	for _, value := range image.RepoDigests {
		parsed, err := reference.ParseNormalizedNamed(value)
		if err == nil && reference.TrimNamed(parsed).Name() == repositoryName {
			immutable = value
			break
		}
	}
	if immutable == "" {
		return planEntry{}, errors.New("active artifact lacks matching immutable repository digest")
	}
	metadata, err := m.api.GetModelInfo(ctx, name)
	if err != nil {
		return planEntry{}, err
	}
	immutable, err = taggedImmutableReference(repositoryName, metadata.Data.Version, immutable)
	if err != nil {
		return planEntry{}, err
	}
	resolved, err := m.validator.ResolveImage(ctx, immutable, "")
	if err != nil {
		return planEntry{}, err
	}
	if resolved.LocalImageID != imageID {
		return planEntry{}, errors.New("resolved artifact differs from actual running image")
	}
	// Existing container is already running. CandidateValidator only reads model
	// metadata/runtime here and validates its immutable OCI labels and provenance.
	validated, err := m.validator.ValidateContainer(ctx, modelupgrade.ValidateCandidateContainer{ContainerID: registered.ContainerID, Image: resolved, ActiveModelInfo: metadata.Data, RegisteredOutputMode: registered.OutputMode})
	if err != nil {
		return planEntry{}, fmt.Errorf("existing runtime must be unloaded and release-valid: %w", err)
	}
	versionID := entity.InferenceModelVersionID(validated.ModelInfo.ModelID, validated.ModelInfo.Version)
	verifiedDeployment, err := modelupgrade.DeploymentFromCandidate(validated, versionID, time.Now())
	if err != nil {
		return planEntry{}, err
	}
	version, err := m.versions.SelectInferenceModelVersion(ctx, versionID)
	publish := false
	if err != nil {
		if err.Error() != apiError.MissingRecord {
			return planEntry{}, err
		}
		version = entity.InferenceModelVersion{ModelID: validated.ModelInfo.ModelID, Version: validated.ModelInfo.Version, ImageReference: validated.ImageReference, ImageDigest: validated.ImageDigest, Channel: entity.InferenceModelVersionRelease, BaseVersion: validated.ModelInfo.Version, Provenance: verifiedDeployment.Provenance, Status: entity.InferenceModelVersionAvailable}
		version.Normalize()
		version.ID = versionID
		if err := version.Validate(); err != nil {
			return planEntry{}, err
		}
		publish = true
	} else if version.Status != entity.InferenceModelVersionAvailable || version.ModelID != validated.ModelInfo.ModelID || version.Version != validated.ModelInfo.Version || version.ImageDigest != validated.ImageDigest || !reflect.DeepEqual(version.Provenance, verifiedDeployment.Provenance) {
		return planEntry{}, errors.New("catalog entry does not match active immutable artifact")
	}
	if !publish {
		catalog, err := m.validator.ResolveImage(ctx, version.ImageReference, version.ImageDigest)
		if err != nil {
			return planEntry{}, err
		}
		if catalog.LocalImageID != imageID {
			return planEntry{}, errors.New("catalog resolves to different image")
		}
	}
	snapshot, err := m.docker.Client.ContainerInspect(ctx, registered.ContainerID)
	if err != nil {
		return planEntry{}, err
	}
	placementName := name
	if snapshot.Config != nil && snapshot.Config.Labels["ai.heartwise.pacs.gpu-placement-name"] != "" {
		placementName = snapshot.Config.Labels["ai.heartwise.pacs.gpu-placement-name"]
	}
	target := m.docker.GPUAssignments[placementName]
	if target == "" {
		target = m.docker.GPUDefaultUUID
	}
	if err := writeProtected(filepath.Join(m.directory, name+".before.json"), snapshot); err != nil {
		return planEntry{}, err
	}
	return planEntry{Registration: registered, OriginalName: name, LocalImageID: imageID, TargetGPUUUID: target, Version: version, PublishCatalog: publish}, nil
}

func (m *migration) apply(ctx context.Context, manager *modelmanager.Manager, coordinator *modelupgrade.UpgradeCoordinator, entry planEntry) (err error) {
	if err := m.event(entry.OriginalName, "start", nil); err != nil {
		return err
	}
	if entry.PublishCatalog {
		if _, err := m.versions.PublishInferenceModelVersion(ctx, entry.Version); err != nil {
			return err
		}
		if err := m.event(entry.OriginalName, "catalog_published", nil); err != nil {
			return err
		}
	}
	result, err := coordinator.Upgrade(ctx, modelupgrade.UpgradeRequest{TenantID: entry.Registration.TenantID, ModelID: entry.Registration.ID, VersionID: entry.Version.ID, OnStage: func(_ context.Context, stage modelupgrade.UpgradeStage) error {
		return m.event(entry.OriginalName, string(stage), nil)
	}})
	if err != nil {
		_ = m.event(entry.OriginalName, "coordinator_failed", map[string]string{"error": err.Error()})
		return err
	}
	stabilized := false
	defer func() {
		if err != nil && !stabilized {
			recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			err = errors.Join(err, m.recoverActivation(recoveryCtx, entry, result))
		}
	}()
	if err := writeProtected(filepath.Join(m.directory, entry.OriginalName+".activation.json"), result); err != nil {
		return err
	}
	if err := manager.BlockAndDrain(ctx, result.Active.ContainerID); err != nil {
		return err
	}
	if err := m.stabilize(ctx, entry, result); err != nil {
		return err
	}
	stabilized = true // Registration and stable DNS are coherent even if reconciliation fails.
	ids, err := m.models.ListRegisteredModelContainerIDs(ctx)
	if err != nil {
		return err
	}
	if err := manager.Reconcile(ctx, ids); err != nil {
		return err
	}
	return m.event(entry.OriginalName, "complete", map[string]string{"containerId": result.Active.ContainerID, "gpuUuid": entry.TargetGPUUUID})
}

// Recover any failure after coordinator activation but before stable naming.
// Stabilize may already have restored registration; compare before attempting CAS.
func (m *migration) recoverActivation(ctx context.Context, entry planEntry, result modelupgrade.UpgradeResult) error {
	current, err := m.models.SelectInferenceModelByID(ctx, entry.Registration.ID)
	if err != nil {
		return err
	}
	if current.TenantID != entry.Registration.TenantID {
		return errors.New("registration tenant changed during activation recovery")
	}
	if current.ContainerID == result.Active.ContainerID {
		err = m.commands.RestoreInferenceModelRegistration(ctx, repositoryTypes.RestoreInferenceModelRegistration{ID: entry.Registration.ID, TenantID: entry.Registration.TenantID, ExpectedContainerID: result.Active.ContainerID, ExpectedImageReference: result.Active.ImageReference, Previous: result.Previous})
		if err != nil {
			return err
		}
	} else if current.ContainerID != entry.Registration.ContainerID {
		return errors.New("registration changed during activation recovery; refusing to overwrite")
	}
	cleanupErr := m.docker.Client.ContainerStop(ctx, result.Active.ContainerID, dockerContainer.StopOptions{})
	if candidate, inspectErr := m.docker.Client.ContainerInspect(ctx, result.Active.ContainerID); inspectErr == nil {
		for network := range candidate.NetworkSettings.Networks {
			cleanupErr = errors.Join(cleanupErr, m.docker.Client.NetworkDisconnect(ctx, network, result.Active.ContainerID, true))
		}
	}
	cleanupErr = errors.Join(cleanupErr, m.docker.Client.ContainerStart(ctx, entry.Registration.ContainerID, dockerContainer.StartOptions{}))
	journalErr := m.event(entry.OriginalName, "activation_recovered", map[string]string{"previousContainerId": entry.Registration.ContainerID})
	return errors.Join(cleanupErr, journalErr)
}

func (m *migration) stabilize(ctx context.Context, entry planEntry, result modelupgrade.UpgradeResult) (err error) {
	old, err := m.docker.Client.ContainerInspect(ctx, entry.Registration.ContainerID)
	if err != nil {
		return err
	}
	backup := entry.OriginalName + "-gpu-backup-" + entry.Registration.ContainerID[:12]
	disconnected := []string{}
	renamed := false
	candidateRenamed := false
	defer func() {
		if err == nil {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		rollbackErr := m.commands.RestoreInferenceModelRegistration(rollbackCtx, repositoryTypes.RestoreInferenceModelRegistration{ID: entry.Registration.ID, TenantID: entry.Registration.TenantID, ExpectedContainerID: result.Active.ContainerID, ExpectedImageReference: result.Active.ImageReference, Previous: result.Previous})
		rollbackErr = errors.Join(rollbackErr, m.docker.Client.ContainerStop(rollbackCtx, result.Active.ContainerID, dockerContainer.StopOptions{}))
		if candidate, inspectErr := m.docker.Client.ContainerInspect(rollbackCtx, result.Active.ContainerID); inspectErr == nil {
			for network := range candidate.NetworkSettings.Networks {
				rollbackErr = errors.Join(rollbackErr, m.docker.Client.NetworkDisconnect(rollbackCtx, network, result.Active.ContainerID, true))
			}
		}
		if candidateRenamed {
			rollbackErr = errors.Join(rollbackErr, m.docker.Client.ContainerRename(rollbackCtx, result.Active.ContainerID, result.ContainerName))
		}
		if renamed {
			rollbackErr = errors.Join(rollbackErr, m.docker.Client.ContainerRename(rollbackCtx, entry.Registration.ContainerID, entry.OriginalName))
		}
		for _, network := range disconnected {
			settings := old.NetworkSettings.Networks[network]
			rollbackErr = errors.Join(rollbackErr, m.docker.Client.NetworkConnect(rollbackCtx, network, entry.Registration.ContainerID, settings))
		}
		rollbackErr = errors.Join(rollbackErr, m.docker.Client.ContainerStart(rollbackCtx, entry.Registration.ContainerID, dockerContainer.StartOptions{}))
		err = errors.Join(err, rollbackErr)
		_ = m.event(entry.OriginalName, "name_stabilization_failed", map[string]string{"error": err.Error()})
	}()
	if _, err = m.docker.Client.ContainerUpdate(ctx, result.Active.ContainerID, dockerContainer.UpdateConfig{RestartPolicy: old.HostConfig.RestartPolicy}); err != nil {
		return err
	}
	if err = m.docker.Client.ContainerStop(ctx, entry.Registration.ContainerID, dockerContainer.StopOptions{}); err != nil {
		return err
	}
	for network := range old.NetworkSettings.Networks {
		if err = m.docker.Client.NetworkDisconnect(ctx, network, entry.Registration.ContainerID, true); err != nil {
			return err
		}
		disconnected = append(disconnected, network)
	}
	if err = m.docker.Client.ContainerRename(ctx, entry.Registration.ContainerID, backup); err != nil {
		return err
	}
	renamed = true
	if err = m.docker.Client.ContainerRename(ctx, result.Active.ContainerID, entry.OriginalName); err != nil {
		return err
	}
	candidateRenamed = true
	info, err := m.docker.GetContainerInfo(ctx, result.Active.ContainerID)
	if err != nil {
		return err
	}
	if info.GPUUUID != entry.TargetGPUUUID || info.ImageID != entry.LocalImageID {
		return errors.New("new placement/image identity verification failed")
	}
	// Ensure exact-case stable DNS alias exists independently of rename behavior.
	current, err := m.docker.Client.ContainerInspect(ctx, result.Active.ContainerID)
	if err != nil {
		return err
	}
	for network, endpoint := range current.NetworkSettings.Networks {
		aliases := append([]string(nil), endpoint.Aliases...)
		if previous := old.NetworkSettings.Networks[network]; previous != nil {
			for _, alias := range previous.Aliases {
				if alias == entry.Registration.ContainerID || alias == entry.Registration.ContainerID[:12] {
					continue
				}
				present := false
				for _, existing := range aliases {
					if alias == existing {
						present = true
					}
				}
				if !present {
					aliases = append(aliases, alias)
				}
			}
		}
		found := false
		for _, alias := range aliases {
			if alias == entry.OriginalName {
				found = true
			}
		}
		if !found {
			aliases = append(aliases, entry.OriginalName)
		}
		if reflect.DeepEqual(aliases, endpoint.Aliases) {
			continue
		}
		if err = m.docker.Client.NetworkDisconnect(ctx, network, result.Active.ContainerID, true); err != nil {
			return err
		}
		if err = m.docker.Client.NetworkConnect(ctx, network, result.Active.ContainerID, &dockerNetwork.EndpointSettings{Aliases: aliases}); err != nil {
			return err
		}
	}
	return nil
}

func taggedImmutableReference(repositoryName, version, immutable string) (string, error) {
	parsed, err := reference.ParseNormalizedNamed(immutable)
	if err != nil {
		return "", err
	}
	canonical, ok := parsed.(reference.Canonical)
	if !ok || reference.TrimNamed(parsed).Name() != repositoryName {
		return "", errors.New("immutable image repository mismatch")
	}
	result := repositoryName + ":" + version + "@" + canonical.Digest().String()
	if _, err := reference.ParseNormalizedNamed(result); err != nil {
		return "", err
	}
	return result, nil
}

func writeProtected(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (m *migration) event(name, stage string, extra any) error {
	file, err := os.OpenFile(filepath.Join(m.directory, "stages.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(map[string]any{"time": time.Now().UTC(), "actor": m.actor, "model": name, "stage": stage, "data": extra}); err != nil {
		return err
	}
	return file.Sync()
}
