package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/docker/docker/errdefs"
	"github.com/stretchr/testify/require"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
)

type stableGatewayDocker struct {
	dockerTypes.DockerSDKInterface
	infos  map[string]dockerTypes.GetContainerInfoResult
	errors map[string]error
	refs   []string
}

func (d *stableGatewayDocker) GetContainerInfo(_ context.Context, ref string) (dockerTypes.GetContainerInfoResult, error) {
	d.refs = append(d.refs, ref)
	if err := d.errors[ref]; err != nil {
		return dockerTypes.GetContainerInfoResult{}, err
	}
	if info, ok := d.infos[ref]; ok {
		return info, nil
	}
	return dockerTypes.GetContainerInfoResult{}, errdefs.NotFound(errors.New("not found"))
}

type stableGatewayRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	models  []entity.InferenceModel
	listErr error
	authErr error
	lists   int
	checks  []string
}

func (r *stableGatewayRepository) SelectInferenceModels(_ context.Context, tenant string) ([]entity.InferenceModel, error) {
	r.lists++
	return r.models, r.listErr
}

func (r *stableGatewayRepository) SelectInferenceModelByContainer(_ context.Context, tenant, id string) (entity.InferenceModel, error) {
	r.checks = append(r.checks, id)
	if r.authErr != nil {
		return entity.InferenceModel{}, r.authErr
	}
	for _, model := range r.models {
		if model.TenantID == tenant && model.ContainerID == id {
			return model, nil
		}
	}
	return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
}

func TestPreparedGatewayStableNamesFollowCurrentTenantRegistration(t *testing.T) {
	oldID, currentID := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, tc := range []struct {
		name                  string
		oldExists, actualName bool
	}{
		{"retained rollback container", true, false},
		{"old name no longer exists", false, false},
		{"current actual name matches", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := dockerTypes.GetContainerInfoResult{ID: currentID, Name: "/DeepCORO-CTO-candidate-new", PlacementName: "DeepCORO-CTO"}
			if tc.actualName {
				candidate.Name = "/DeepCORO-CTO"
				candidate.PlacementName = ""
			}
			sdk := &stableGatewayDocker{infos: map[string]dockerTypes.GetContainerInfoResult{currentID: candidate}}
			if tc.oldExists {
				sdk.infos["deepcoro-cto"] = dockerTypes.GetContainerInfoResult{ID: oldID, Name: "/DeepCORO-CTO"}
			}
			repo := &stableGatewayRepository{models: []entity.InferenceModel{{TenantID: "tenant-a", ContainerID: currentID}}}
			predictor := &recordingManagedPredictor{}
			service := &InferenceCommandService{DockerSDKInterface: sdk, InferenceQueryRepositoryInterface: repo, ModelManager: predictor}
			_, err := service.PredictPreparedInferenceModel(context.Background(), "tenant-a", "deepcoro-cto", api.PredictRequest{})
			require.NoError(t, err)
			require.Equal(t, currentID, predictor.containerID)
			require.Equal(t, 1, predictor.calls)
			require.Equal(t, currentID, repo.checks[len(repo.checks)-1])
		})
	}
}

func TestPreparedGatewayStableResolutionFailsClosed(t *testing.T) {
	oldID, id1, id2 := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, scenario := range []string{"cross tenant", "ambiguous labels", "daemon failure", "authorization unavailable", "registration list unavailable", "candidate inspect unavailable", "canonical mismatch", "authorization changed"} {
		t.Run(scenario, func(t *testing.T) {
			sdk := &stableGatewayDocker{infos: map[string]dockerTypes.GetContainerInfoResult{
				"stable-model": {ID: oldID, Name: "/stable-model"},
				id1:            {ID: id1, Name: "/candidate-one", PlacementName: "stable-model"},
			}, errors: map[string]error{}}
			repo := &stableGatewayRepository{models: []entity.InferenceModel{{TenantID: "tenant-a", ContainerID: id1}}}
			switch scenario {
			case "cross tenant":
				repo.models[0].TenantID = "tenant-b"
			case "ambiguous labels":
				repo.models = append(repo.models, entity.InferenceModel{TenantID: "tenant-a", ContainerID: id2})
				sdk.infos[id2] = dockerTypes.GetContainerInfoResult{ID: id2, Name: "/candidate-two", PlacementName: "STABLE-MODEL"}
			case "daemon failure":
				sdk.errors["stable-model"] = errors.New("daemon unavailable")
			case "authorization unavailable":
				repo.authErr = errors.New("database unavailable")
			case "registration list unavailable":
				repo.listErr = errors.New("database unavailable")
			case "candidate inspect unavailable":
				sdk.errors[id1] = errors.New("daemon unavailable")
			case "canonical mismatch":
				sdk.infos[id1] = dockerTypes.GetContainerInfoResult{ID: id2, Name: "/candidate-one", PlacementName: "stable-model"}
			case "authorization changed":
				repo.authErr = errors.New(apiError.MissingRecord)
			}
			predictor := &recordingManagedPredictor{}
			service := &InferenceCommandService{DockerSDKInterface: sdk, InferenceQueryRepositoryInterface: repo, ModelManager: predictor}
			_, err := service.PredictPreparedInferenceModel(context.Background(), "tenant-a", "stable-model", api.PredictRequest{})
			require.Error(t, err)
			require.Zero(t, predictor.calls)
			if scenario == "cross tenant" {
				require.NotContains(t, sdk.refs, id1)
			}
			if scenario == "authorization unavailable" || scenario == "daemon failure" {
				require.Zero(t, repo.lists)
			}
		})
	}
}

func TestPreparedGatewayNeverUsesIDsOrInvalidReferencesAsAliases(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, ref := range []string{id, id[:12], id[:8], "http://stable-model:8000", "stable-model:8000", "../stable-model", "model/name"} {
		t.Run(ref, func(t *testing.T) {
			sdk := &stableGatewayDocker{}
			repo := &stableGatewayRepository{}
			predictor := &recordingManagedPredictor{}
			service := &InferenceCommandService{DockerSDKInterface: sdk, InferenceQueryRepositoryInterface: repo, ModelManager: predictor}
			_, err := service.PredictPreparedInferenceModel(context.Background(), "tenant-a", ref, api.PredictRequest{})
			require.Error(t, err)
			require.Zero(t, repo.lists)
			require.Zero(t, predictor.calls)
			if !preparedContainerName.MatchString(ref) {
				require.Empty(t, sdk.refs)
			}
		})
	}
}

func TestPreparedGatewayAuthorizedIDsAndNamesKeepDirectPath(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, ref := range []string{id, id[:12], "current-model"} {
		t.Run(ref, func(t *testing.T) {
			sdk := &stableGatewayDocker{infos: map[string]dockerTypes.GetContainerInfoResult{
				ref: {ID: id, Name: "/current-model", PlacementName: "stable-model"},
			}}
			repo := &stableGatewayRepository{models: []entity.InferenceModel{{TenantID: "tenant-a", ContainerID: id}}}
			predictor := &recordingManagedPredictor{}
			service := &InferenceCommandService{DockerSDKInterface: sdk, InferenceQueryRepositoryInterface: repo, ModelManager: predictor}
			_, err := service.PredictPreparedInferenceModel(context.Background(), "tenant-a", ref, api.PredictRequest{})
			require.NoError(t, err)
			require.Equal(t, id, predictor.containerID)
			require.Equal(t, []string{ref}, sdk.refs)
			require.Zero(t, repo.lists)
		})
	}
}
