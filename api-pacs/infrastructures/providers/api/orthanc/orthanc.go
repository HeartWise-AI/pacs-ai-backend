package orthanc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// GetModalityStudies get modality studies
func (o *OrthancAPI) GetModalityStudies(ctx context.Context, aet string, requestPayload types.QueryModalitiesRequest) ([]types.QueryModalitiesAnswersResponse, string, error) {
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

	var answerResponse types.RetrieveQueryModalityAnswerResponse
	if err := json.NewDecoder(resp.Body).Decode(&answerResponse); err != nil {
		return types.RetrieveQueryModalityAnswerResponse{}, err
	}

	return answerResponse, nil
}
