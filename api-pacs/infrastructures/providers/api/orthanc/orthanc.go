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
	"time"

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
func (o *OrthancAPI) DeleteLocalResources(ctx context.Context, requestPayload types.DeleteLocalResourcesRequest) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(requestPayload)
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
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(map[string]interface{}{
		"Level":  "Study",
		"Query":  map[string]interface{}{},
		"Expand": true,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tools/find", o.BaseURL), buf)
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

	var localResources []types.GetLocalStudyResponse
	if err := json.NewDecoder(resp.Body).Decode(&localResources); err != nil {
		return nil, err
	}

	if len(localResources) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return localResources, nil
}

// FindLocalResource find local resource
func (o *OrthancAPI) FindLocalResource(ctx context.Context, requestPayload types.QueryLocalResourceRequest) ([]string, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(requestPayload)
	if err != nil {
		return nil, err
	}

	fmt.Println(requestPayload)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tools/find", o.BaseURL), buf)
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

	var queryIDs []string
	if err := json.NewDecoder(resp.Body).Decode(&queryIDs); err != nil {
		return nil, err
	}

	if len(queryIDs) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return queryIDs, nil
}

// FindModalityStudies find modality studies
func (o *OrthancAPI) FindModalityStudies(ctx context.Context, aet string, requestPayload types.QueryModalitiesRequest) ([]types.QueryModalitiesAnswersResponse, string, error) {
	// query modalities
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(requestPayload)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/query", o.BaseURL, aet), buf)
	if err != nil {
		return nil, "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, "", err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return nil, "", errors.New(apiError.OrthancError)
	}

	var queryModalitiesResponse types.QueryModalitiesResponse
	if err := json.NewDecoder(resp.Body).Decode(&queryModalitiesResponse); err != nil {
		return nil, "", err
	}

	if len(queryModalitiesResponse.ID) == 0 {
		return nil, "", errors.New(apiError.MissingRecord)
	}

	// query answers
	req1, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/queries/%s/answers?expand=true&simplify=true", o.BaseURL, queryModalitiesResponse.ID), nil)
	if err != nil {
		return nil, "", err
	}

	resp1, err := client.Do(req1)
	if err != nil {
		return nil, "", err
	}
	defer resp1.Body.Close()

	if resp1.StatusCode < 200 || resp1.StatusCode > 299 {
		response, err := io.ReadAll(resp1.Body)
		if err != nil {
			return nil, "", err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return nil, "", errors.New(apiError.OrthancError)
	}

	var queryModalitiesAnswersResponse []types.QueryModalitiesAnswersResponse
	if err := json.NewDecoder(resp1.Body).Decode(&queryModalitiesAnswersResponse); err != nil {
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
		return types.GetJobResponse{}, err
	}

	return job, nil
}

// RetrieveModalityStudy retrieve modality study
func (o *OrthancAPI) RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint, requestPayload types.RetrieveQueryModalityAnswerRequest) (types.RetrieveQueryModalityAnswerResponse, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(requestPayload)
	if err != nil {
		return types.RetrieveQueryModalityAnswerResponse{}, err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/queries/%s/answers/%d/retrieve", o.BaseURL, queryID, answerIndex), buf)
	if err != nil {
		return types.RetrieveQueryModalityAnswerResponse{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return types.RetrieveQueryModalityAnswerResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return types.RetrieveQueryModalityAnswerResponse{}, err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return types.RetrieveQueryModalityAnswerResponse{}, errors.New(apiError.OrthancError)
	}

	var answerResponse types.RetrieveQueryModalityAnswerResponse
	if err := json.NewDecoder(resp.Body).Decode(&answerResponse); err != nil {
		return types.RetrieveQueryModalityAnswerResponse{}, err
	}

	return answerResponse, nil
}
