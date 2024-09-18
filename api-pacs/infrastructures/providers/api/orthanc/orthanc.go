package orthanc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
)

// TODO: try nginx-based header checks for orthanc? https://stackoverflow.com/a/58066868/10245760
type OrthancAPI struct {
	BaseURL string
}

var (
	client *http.Client = &http.Client{Timeout: 5 * time.Minute}
)

// Init initialize orthanc connection
func Init(baseURL string) *OrthancAPI {
	return &OrthancAPI{
		BaseURL: baseURL,
	}
}

// DeleteLocalResources delete local resources
func (o *OrthancAPI) DeleteLocalResources(ctx context.Context, request types.DeleteLocalResourcesRequest) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tools/bulk-delete", o.BaseURL), buf)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return errors.New(apiError.OrthancError)
	}

	return nil
}

// DownloadDICOM download dicom by query ID
func (o *OrthancAPI) DownloadDICOM(ctx context.Context, queryID string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/instances/%s/file", o.BaseURL, queryID), nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		errorMessage := string(response)
		log.Println("Error:", errorMessage)
		return nil, errors.New(apiError.OrthancError)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return bodyBytes, nil
}

// FindLocalStudies find local studies from orthanc
func (o *OrthancAPI) FindLocalStudies(ctx context.Context) ([]types.GetLocalStudyResponse, error) {
	payload := map[string]interface{}{
		"Level":  "Study",
		"Query":  map[string]interface{}{},
		"Expand": true,
	}

	var localResources []types.GetLocalStudyResponse
	err := o.findLocalResources(payload, &localResources)
	if err != nil {
		return nil, err
	}

	if len(localResources) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return localResources, nil
}

// FindLocalResource find local resources
func (o *OrthancAPI) FindLocalResources(ctx context.Context, request types.QueryLocalResourceRequest) ([]string, error) {
	var queryIDs []string
	err := o.findLocalResources(request, &queryIDs)
	if err != nil {
		return nil, err
	}

	if len(queryIDs) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return queryIDs, nil
}

// FindModalityStudies find modality studies
func (o *OrthancAPI) FindModalityStudies(ctx context.Context, AET string, request types.QueryModalitiesRequest) ([]types.QueryModalityStudyAnswersResponse, string, error) {
	// query modalities
	var queryModalitiesResponse types.QueryModalityResponse
	if err := o.queryModalities(AET, request, &queryModalitiesResponse); err != nil {
		return nil, "", err
	}

	if len(queryModalitiesResponse.ID) == 0 {
		return nil, "", errors.New(apiError.MissingRecord)
	}

	// query answers
	var queryModalitiesAnswersResponse []types.QueryModalityStudyAnswersResponse
	err := o.findQueryAnswers(queryModalitiesResponse.ID, &queryModalitiesAnswersResponse)
	if err != nil {
		return nil, "", err
	}

	if len(queryModalitiesAnswersResponse) == 0 {
		return nil, "", errors.New(apiError.MissingRecord)
	}

	return queryModalitiesAnswersResponse, queryModalitiesResponse.ID, nil
}

// GetJobInfo get job info
func (o *OrthancAPI) GetJobInfo(ctx context.Context, jobID string) (types.GetJobResponse, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/jobs/%s", o.BaseURL, jobID), nil)
	if err != nil {
		return types.GetJobResponse{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return types.GetJobResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return types.GetJobResponse{}, err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return types.GetJobResponse{}, errors.New(apiError.OrthancError)
	}

	var job types.GetJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		log.Println("Error:", err)
		return types.GetJobResponse{}, err
	}

	return job, nil
}

// RetrieveModalityStudy retrieve modality study
// Retrieve entire study (old implementation)
func (o *OrthancAPI) RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint, request types.RetrieveQueryModalityAnswerRequest) (types.QueryModalityResponse, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(request)
	if err != nil {
		return types.QueryModalityResponse{}, err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/queries/%s/answers/%d/retrieve", o.BaseURL, queryID, answerIndex), buf)
	if err != nil {
		return types.QueryModalityResponse{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return types.QueryModalityResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return types.QueryModalityResponse{}, err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return types.QueryModalityResponse{}, errors.New(apiError.OrthancError)
	}

	var answerResponse types.QueryModalityResponse
	if err := json.NewDecoder(resp.Body).Decode(&answerResponse); err != nil {
		log.Println("Error:", err)
		return types.QueryModalityResponse{}, err
	}

	return answerResponse, nil
}

// RetrieveModalityStudyBySeries retrieve modality study by series
func (o *OrthancAPI) RetrieveModalityStudyBySeries(ctx context.Context, AET string, request types.RetrieveModalityStudyBySeriesRequest) ([]types.QueryModalityResponse, error) {
	// query modalities
	var queryModalitySeriesResponse types.QueryModalityResponse
	if err := o.queryModalities(AET, request, &queryModalitySeriesResponse); err != nil {
		return nil, err
	}

	if len(queryModalitySeriesResponse.ID) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	// query answers
	var queryModalitySeriesAnswersResponse []types.QueryModalitySeriesAnswersResponse
	err := o.findQueryAnswers(queryModalitySeriesResponse.ID, &queryModalitySeriesAnswersResponse)
	if err != nil {
		return nil, err
	}

	if len(queryModalitySeriesAnswersResponse) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	// retrieve by series concurrently
	var m = sync.Mutex{}
	eg, _ := errgroup.WithContext(ctx)

	var results []types.QueryModalityResponse

	// set limit
	eg.SetLimit(len(queryModalitySeriesAnswersResponse))

	for _, series := range queryModalitySeriesAnswersResponse {
		m.Lock()

		func(series types.QueryModalitySeriesAnswersResponse) {
			eg.Go(func() error {
				defer m.Unlock()

				// retrieve by series (c-move)
				buf := new(bytes.Buffer)
				err := json.NewEncoder(buf).Encode(map[string]interface{}{
					"Level": "Series",
					"Resources": []map[string]string{
						{
							"StudyInstanceUID":  series.StudyInstanceUID,
							"SeriesInstanceUID": series.SeriesInstanceUID,
						},
					},
					"Asynchronous": true,
					"Full":         true,
					"Permissive":   true,
					"Priority":     0,
					"Simplify":     true,
					"Synchronous":  false,
					"TargetAet":    request.LocalAet,
					"Timeout":      0,
				})
				if err != nil {
					return err
				}

				req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/move", o.BaseURL, AET), buf)
				if err != nil {
					return err
				}

				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()

				if resp.StatusCode < 200 || resp.StatusCode > 299 {
					response, err := io.ReadAll(resp.Body)
					if err != nil {
						return err
					}
					errorMessage := string(response)

					log.Println("Error:", errorMessage)
					return errors.New(apiError.OrthancError)
				}

				var answerResponse types.QueryModalityResponse
				if err := json.NewDecoder(resp.Body).Decode(&answerResponse); err != nil {
					return err
				}

				results = append(results, answerResponse)
				return nil
			})
		}(series)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		log.Println("Error:", err)
		return nil, err
	}

	return results, nil
}

func (o *OrthancAPI) findLocalResources(request, response interface{}) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tools/find", o.BaseURL), buf)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return errors.New(apiError.OrthancError)
	}

	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		log.Println("Error:", err)
		return err
	}

	return nil
}

func (o *OrthancAPI) findQueryAnswers(queryID string, response interface{}) error {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/queries/%s/answers?expand=true&simplify=true", o.BaseURL, queryID), nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return errors.New(apiError.OrthancError)
	}

	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		log.Println("Error:", err)
		return errors.New(apiError.OrthancError)
	}

	return nil
}

func (o *OrthancAPI) queryModalities(AET string, request interface{}, response *types.QueryModalityResponse) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/query", o.BaseURL, AET), buf)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return errors.New(apiError.OrthancError)
	}

	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		log.Println("Error:", err)
		return errors.New(apiError.OrthancError)
	}

	return nil
}
