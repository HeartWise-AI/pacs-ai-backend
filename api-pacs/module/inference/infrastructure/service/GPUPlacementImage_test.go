package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

const (
	placementLocalImageID    = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	placementOfficialImageID = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	placementOfficialDigest  = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	placementLocalImage      = "model-local:1.0.1-render-report-1"
	placementOfficialImage   = "heartwisehub/model:1.0.1"
)

// imageDocker adds image inspection to the administration Docker fake.
type imageDocker struct {
	*adminTestDocker
	images map[string]dockerTypes.InspectImageResult
}

func (d *imageDocker) InspectImage(_ context.Context, reference string) (dockerTypes.InspectImageResult, error) {
	if image, ok := d.images[reference]; ok {
		return image, nil
	}
	return dockerTypes.InspectImageResult{}, errors.New("No such image: " + reference)
}

type placementVersions struct{}

func (placementVersions) SelectInferenceModelVersion(_ context.Context, id string) (entity.InferenceModelVersion, error) {
	return entity.InferenceModelVersion{ID: id, ImageReference: placementOfficialImage, ImageDigest: placementOfficialDigest}, nil
}

// withImages makes every model run the published image, except a2: the model
// the plan moves runs a local build that the catalog does not know.
func (f *placementFixture) withImages() *imageDocker {
	official := dockerTypes.InspectImageResult{ID: placementOfficialImageID, RepoTags: []string{placementOfficialImage}, RepoDigests: []string{"heartwisehub/model@" + placementOfficialDigest}}
	local := dockerTypes.InspectImageResult{ID: placementLocalImageID, RepoTags: []string{placementLocalImage}}
	docker := &imageDocker{adminTestDocker: f.docker, images: map[string]dockerTypes.InspectImageResult{
		placementOfficialImageID: official, placementOfficialImage: official, placementLocalImageID: local, placementLocalImage: local,
	}}
	for id, info := range f.docker.infos {
		info.ImageID, info.ImageReference = placementOfficialImageID, placementOfficialImage
		if id == placementID(2) {
			info.ImageID, info.ImageReference = placementLocalImageID, placementLocalImage
		}
		f.docker.infos[id] = info
	}
	f.service.Docker, f.service.Versions = docker, placementVersions{}
	return docker
}

func TestPlacementPlanReportsAModelWhoseRunningImageIsNotTheRegisteredOne(t *testing.T) {
	fixture := newPlacementFixture(t, nil)
	fixture.withImages()
	plan, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.True(t, plan.ImageDrift)
	require.Equal(t, []string{"model-a2"}, plan.ImageDriftModels)
	require.Len(t, plan.Moves, 1)
	move := plan.Moves[0]
	require.Equal(t, "model-a2", move.ModelID)
	require.True(t, move.ImageDrift)
	require.Equal(t, placementLocalImage, move.RunningImage)
	require.Equal(t, placementLocalImageID, move.RunningImageID)
	require.Equal(t, placementOfficialImage, move.RegisteredImage)
	require.Empty(t, move.BlockedReason, "a move keeps the running image, so drift does not block it")
	for _, model := range plan.Models {
		require.Equal(t, model.ModelID == "model-a2", model.ImageDrift, model.ModelID)
		require.False(t, model.RunningImageUnavailable)
	}
	status, err := fixture.service.Snapshot(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.True(t, status.Models[1].ImageDrift)
	require.Equal(t, placementOfficialImage, status.Models[2].RunningImage)
	require.False(t, status.Models[2].ImageDrift)
}

func TestAutomaticMoveKeepsTheRunningImageOfADriftedModel(t *testing.T) {
	fixture := newPlacementFixture(t, nil)
	fixture.withImages()
	require.Contains(t, fixture.service.autoPlacementCycle(context.Background()), "moved model model-a2")
	require.Len(t, fixture.deployer.requests, 1)
	request := fixture.deployer.requests[0]
	require.True(t, request.KeepRunningImage, "a move must never deploy the registered image over a running one")
	require.True(t, request.Automatic)
	require.Equal(t, placementID(2), request.ExpectedContainerID)
}

func TestAutomaticMoveIsNeverAppliedWhenTheRunningImageIsGone(t *testing.T) {
	fixture := newPlacementFixture(t, nil)
	docker := fixture.withImages()
	delete(docker.images, placementLocalImageID) // the local build was deleted from the host
	delete(docker.images, placementLocalImage)

	plan, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.True(t, plan.Moves[0].RunningImageUnavailable)
	require.Contains(t, plan.Moves[0].BlockedReason, "would deploy the registered image "+placementOfficialImage)
	require.Contains(t, fixture.service.autoPlacementCycle(context.Background()), "no planned move is safe now")
	require.Empty(t, fixture.deployer.requests)

	// The guard is also in the move itself, not only in the plan.
	_, err = fixture.service.move(context.Background(), "tenant-a", "system", "model-a2", adminGPUB, placementID(2), true)
	require.EqualError(t, err, apiError.InferenceUpgradeConflict)
	require.Empty(t, fixture.deployer.requests)

	// An administrator may still move it; the deployment then falls back to
	// the registered image and says so.
	_, err = fixture.service.Move(context.Background(), "tenant-a", "operator", "model-a2", adminGPUB, placementID(2))
	require.NoError(t, err)
	require.Len(t, fixture.deployer.requests, 1)
	require.True(t, fixture.deployer.requests[0].KeepRunningImage)
	require.False(t, fixture.deployer.requests[0].Automatic)
}

func TestAutomaticMoveLooksAtTheModelAgainJustBeforeMoving(t *testing.T) {
	fixture := newPlacementFixture(t, nil)
	fixture.backend.hold, fixture.backend.started = make(chan struct{}), make(chan string, 1)
	// The request arrives after the plan was computed: the plan reads the
	// clock once, the cycle reads it again just before it looks at the model.
	var busy <-chan error
	calls := 0
	clock := fixture.clock
	fixture.service.Clock = func() time.Time {
		if calls++; calls == 2 {
			busy = fixture.predict(t, 2)
			<-fixture.backend.started
		}
		return clock
	}
	outcome := fixture.service.autoPlacementCycle(context.Background())
	require.NotNil(t, busy)
	require.Contains(t, outcome, "no planned move is safe now")
	require.Contains(t, outcome, "serving or has waiting requests")
	require.Empty(t, fixture.deployer.requests)
	close(fixture.backend.hold)
	require.NoError(t, <-busy)
}
