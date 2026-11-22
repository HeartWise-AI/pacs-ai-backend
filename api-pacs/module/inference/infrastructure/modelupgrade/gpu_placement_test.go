package modelupgrade

import (
	"context"
	"errors"
	"testing"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/module/inference/infrastructure/modelmanager"
)

const explicitTestGPU = "GPU-999e1fca-539d-fb99-2531-ad97b91da026"

type placementCoordinatorManager struct {
	*coordinatorManager
	target       string
	placementErr error
	preference   string
}

type unreconciledCoordinatorManager struct{ *coordinatorManager }

func (*unreconciledCoordinatorManager) Snapshot() modelmanager.Snapshot {
	return modelmanager.Snapshot{Ready: false}
}

func TestUpgradeCannotStageCandidateBeforeInventoryReconciles(t *testing.T) {
	f := newCoordinatorFixture(t)
	f.coordinator.manager = &unreconciledCoordinatorManager{f.manager}
	_, err := f.upgrade(t)
	if !errors.Is(err, modelmanager.ErrNotReconciled) {
		t.Fatalf("expected closed GPU admission, got %v", err)
	}
	if len(*f.events) != 0 {
		t.Fatalf("unreconciled upgrade touched providers: %v", *f.events)
	}
}

func (manager *placementCoordinatorManager) ValidateGPUPreference(uuid string, _ api.ModelResources) error {
	manager.preference = uuid
	return nil
}

func (manager *placementCoordinatorManager) ValidateGPUPlacement(uuid string, _ api.ModelResources) error {
	manager.target = uuid
	return manager.placementErr
}
func TestSameArtifactDeploymentPersistsExplicitGPUAndExpectedSource(t *testing.T) {
	f := newCoordinatorFixture(t)
	manager := &placementCoordinatorManager{coordinatorManager: f.manager}
	f.coordinator.manager = manager
	f.validator.gpuUUID = explicitTestGPU
	result, err := f.coordinator.Upgrade(context.Background(), UpgradeRequest{TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version-2", TargetGPUUUID: explicitTestGPU, ExpectedContainerID: testActiveContainerID})
	if err != nil {
		t.Fatal(err)
	}
	if f.docker.created.GPUUUID != explicitTestGPU || manager.target != explicitTestGPU || result.Active.GPUUUID != explicitTestGPU || f.registrations.replaced.Deployment.GPUUUID != explicitTestGPU {
		t.Fatal("GPU selection did not survive deployment pipeline")
	}
}
func TestDeploymentRejectsStaleSourceBeforeCreatingCandidate(t *testing.T) {
	f := newCoordinatorFixture(t)
	_, err := f.coordinator.Upgrade(context.Background(), UpgradeRequest{TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version-2", ExpectedContainerID: "stale"})
	if err == nil || f.docker.created.Name != "" {
		t.Fatalf("stale placement started: %+v %v", f.docker.created, err)
	}
}
func TestFutureUpgradePreservesStoredGPUPreference(t *testing.T) {
	f := newCoordinatorFixture(t)
	manager := &placementCoordinatorManager{coordinatorManager: f.manager}
	manager.placementErr = errors.New("explicit placement unavailable under scalar budget")
	f.coordinator.manager = manager
	f.coordinator.models.(*coordinatorModels).model.PreferredGPUUUID = explicitTestGPU
	f.validator.gpuUUID = explicitTestGPU
	if _, err := f.upgrade(t); err != nil {
		t.Fatal(err)
	}
	if f.docker.created.GPUUUID != explicitTestGPU || manager.preference != explicitTestGPU || manager.target != "" {
		t.Fatal("future upgrade lost registration preference")
	}
}
func TestGPUPlacementMustBeAllowedAndActuallyApplied(t *testing.T) {
	for _, reject := range []bool{false, true} {
		f := newCoordinatorFixture(t)
		manager := &placementCoordinatorManager{coordinatorManager: f.manager}
		if reject {
			manager.placementErr = errors.New("not configured")
		}
		f.coordinator.manager = manager
		_, err := f.coordinator.Upgrade(context.Background(), UpgradeRequest{TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version-2", TargetGPUUUID: explicitTestGPU})
		if err == nil {
			t.Fatal("invalid or unapplied GPU target accepted")
		}
	}
}
