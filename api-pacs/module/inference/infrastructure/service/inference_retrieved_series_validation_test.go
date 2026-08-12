package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type retrievedSeriesQueryRepository struct {
	repository.InferenceQueryRepositoryInterface
}

func (repository *retrievedSeriesQueryRepository) SelectInferenceModelByContainer(context.Context, string, string) (entity.InferenceModel, error) {
	return entity.InferenceModel{OutputMode: entity.OutputModeJSON}, nil
}

type retrievedSeriesDockerSDK struct {
	dockerTypes.DockerSDKInterface
}

func (sdk *retrievedSeriesDockerSDK) GetContainerInfo(context.Context, string) (dockerTypes.GetContainerInfoResult, error) {
	return dockerTypes.GetContainerInfoResult{Name: "/model-container"}, nil
}

type retrievedSeriesDockerInferenceAPI struct {
	dockerInferenceTypes.DockerInferenceAPIInterface
}

func (api *retrievedSeriesDockerInferenceAPI) GetModelInfo(context.Context, string) (dockerInferenceTypes.GetModelInfoResponse, error) {
	response := dockerInferenceTypes.GetModelInfoResponse{}
	response.Data.DicomUploadMin = 3
	response.Data.DicomUploadMax = 3
	response.Data.SupportedDicomTags = []string{"*"}
	return response, nil
}

type retrievedSeriesOrthancAPI struct {
	orthancAPITypes.OrthancAPIInterface
	emptySeriesUID string
}

func (api *retrievedSeriesOrthancAPI) GetDICOMWebSeriesInstances(_ context.Context, _ string, seriesInstanceUID string) ([]map[string]interface{}, error) {
	if seriesInstanceUID == api.emptySeriesUID {
		return []map[string]interface{}{}, nil
	}
	return []map[string]interface{}{dicomInstanceForRetrievedSeriesTest(seriesInstanceUID)}, nil
}

func (api *retrievedSeriesOrthancAPI) RetrieveDICOMWebInstanceFile(context.Context, string, string, string) ([]byte, error) {
	return []byte("dicom"), nil
}

func dicomInstanceForRetrievedSeriesTest(seriesInstanceUID string) map[string]interface{} {
	seriesNumber := float64(1)
	switch seriesInstanceUID {
	case "1.2.3.2":
		seriesNumber = 2
	case "1.2.3.3":
		seriesNumber = 3
	}
	return map[string]interface{}{
		"00200011": map[string]interface{}{"Value": []interface{}{seriesNumber}},
		"00080018": map[string]interface{}{"Value": []interface{}{seriesInstanceUID + ".1"}},
		"00080016": map[string]interface{}{"Value": []interface{}{"1.2.840.10008.5.1.4.1.1.2"}},
		"00200013": map[string]interface{}{"Value": []interface{}{float64(1)}},
	}
}

func TestGeneratePredictRequestRejectsWhenRetrievedSeriesFallsBelowModelMinimum(t *testing.T) {
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: &retrievedSeriesQueryRepository{},
		DockerSDKInterface:                &retrievedSeriesDockerSDK{},
		DockerInferenceAPIInterface:       &retrievedSeriesDockerInferenceAPI{},
		OrthancAPIInterface:               &retrievedSeriesOrthancAPI{emptySeriesUID: "1.2.3.3"},
	}

	_, _, err := service.GenerateInferenceModelPredictRequest(
		context.Background(),
		"tenant-a",
		"container-a",
		serviceTypes.PredictInferenceModel{
			StudyInstanceUID:   "1.2.3",
			SeriesInstanceUIDs: []string{"1.2.3.1", "1.2.3.2", "1.2.3.3"},
		},
	)

	var boundsError *apiError.InferenceModelInputError
	require.ErrorAs(t, err, &boundsError)
	require.Equal(t, apiError.InferenceModelInputOutOfRange, boundsError.ErrorCode)
	require.Equal(t, 3, boundsError.Minimum)
	require.Equal(t, 3, boundsError.Maximum)
	require.Equal(t, 2, boundsError.Actual)
}

func TestGeneratePredictRequestAcceptsAllUsableRetrievedSeries(t *testing.T) {
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: &retrievedSeriesQueryRepository{},
		DockerSDKInterface:                &retrievedSeriesDockerSDK{},
		DockerInferenceAPIInterface:       &retrievedSeriesDockerInferenceAPI{},
		OrthancAPIInterface:               &retrievedSeriesOrthancAPI{},
	}

	request, containerName, err := service.GenerateInferenceModelPredictRequest(
		context.Background(),
		"tenant-a",
		"container-a",
		serviceTypes.PredictInferenceModel{
			StudyInstanceUID:   "1.2.3",
			SeriesInstanceUIDs: []string{"1.2.3.1", "1.2.3.2", "1.2.3.3"},
		},
	)

	require.NoError(t, err)
	require.Equal(t, "model-container", containerName)
	require.Len(t, request.SeriesInstanceImages, 3)
}
