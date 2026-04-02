package orthanc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
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

// DeleteDICOMModality delete dicom modality
func (o *OrthancAPI) DeleteDICOMModality(ctx context.Context, modalityID string) error {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/modalities/%s", o.BaseURL, modalityID), nil)
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

// FindLocalResources find local resources from orthanc
func (o *OrthancAPI) FindLocalResources(ctx context.Context, request types.QueryLocalResourceRequest) ([]types.GetLocalResourceResponse, error) {
	var localResources []types.GetLocalResourceResponse
	err := o.findLocalResources(request, &localResources)
	if err != nil {
		return nil, err
	}

	if len(localResources) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return localResources, nil
}

// FindLocalSOPInstance find local SOP instance
func (o *OrthancAPI) FindLocalSOPInstance(ctx context.Context, sopInstanceUID string) ([]string, error) {
	var queryIDs []string
	err := o.findLocalResources(map[string]interface{}{
		"Level": "Instance",
		"Query": map[string]string{
			"SOPInstanceUID": sopInstanceUID,
		},
	}, &queryIDs)
	if err != nil {
		return nil, err
	}

	if len(queryIDs) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return queryIDs, nil
}

// FindModalityStudies find modality studies
func (o *OrthancAPI) FindModalityStudies(ctx context.Context, modalityID string, request types.QueryModalitiesRequest) ([]types.QueryModalityStudyAnswersResponse, string, error) {
	// query modalities
	var queryModalitiesResponse types.QueryModalityResponse
	if err := o.queryModalities(modalityID, request, &queryModalitiesResponse); err != nil {
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

// FindModalitySeriesByStudy find modality series by study
func (o *OrthancAPI) FindModalitySeriesByStudy(ctx context.Context, modalityID, localAET, studyInstanceUID string) ([]types.QueryModalitySeriesAnswersResponse, error) {
	// query modalities
	request := map[string]interface{}{
		"Level":     "Series",
		"LocalAet":  localAET,
		"Normalize": true,
		"Query": map[string]string{
			"StudyInstanceUID": studyInstanceUID,
		},
		"Timeout": 0,
	}

	var queryModalitySeriesResponse types.QueryModalityResponse
	if err := o.queryModalities(modalityID, request, &queryModalitySeriesResponse); err != nil {
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

	return queryModalitySeriesAnswersResponse, nil
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

// GetDICOMWebSeriesInstances get DICOM web series instances
func (o *OrthancAPI) GetDICOMWebSeriesInstances(ctx context.Context, studyInstanceUID, seriesInstanceUID string) ([]map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/dicom-web/studies/%s/series/%s/instances", o.BaseURL, studyInstanceUID, seriesInstanceUID), nil)
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

	var instances []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		log.Println("Error:", err)
		return nil, err
	}

	return instances, nil
}

// ListDICOMModalities list dicom modalities
func (o *OrthancAPI) ListDICOMModalities(ctx context.Context) (map[string]types.ListDICOMModalitiesResponse, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/modalities?expand=true", o.BaseURL), nil)
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

	listDICOMModalitiesResponse := map[string]types.ListDICOMModalitiesResponse{}

	if err := json.NewDecoder(resp.Body).Decode(&listDICOMModalitiesResponse); err != nil {
		log.Println("Error:", err)
		return nil, err
	}

	return listDICOMModalitiesResponse, nil
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
func (o *OrthancAPI) RetrieveModalityStudyBySeries(ctx context.Context, modalityID, localAet, studyInstanceUID string) ([]types.QueryModalityResponse, error) {
	// get modality series by study
	queryModalitySeriesAnswersResponse, err := o.FindModalitySeriesByStudy(ctx, modalityID, localAet, studyInstanceUID)
	if err != nil {
		return nil, err
	}

	// retrieve by series concurrently
	var m = sync.Mutex{}
	eg, _ := errgroup.WithContext(ctx)

	var results []types.QueryModalityResponse

	// set limit
	eg.SetLimit(len(queryModalitySeriesAnswersResponse))

	for _, series := range queryModalitySeriesAnswersResponse {
		func(series types.QueryModalitySeriesAnswersResponse) {
			eg.Go(func() error {
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
					"TargetAet":    localAet,
					"Timeout":      0,
				})
				if err != nil {
					return err
				}

				req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/move", o.BaseURL, modalityID), buf)
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

				m.Lock()
				results = append(results, answerResponse)
				defer m.Unlock()

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

// RetrieveModalityStudyByInstances retrieve modality study by instances (for US modalities)
func (o *OrthancAPI) RetrieveModalityStudyByInstances(ctx context.Context, modalityID, localAet, studyInstanceUID string) ([]types.QueryModalityResponse, error) {
	// get modality series by study
	queryModalitySeriesAnswersResponse, err := o.FindModalitySeriesByStudy(ctx, modalityID, localAet, studyInstanceUID)
	if err != nil {
		return nil, err
	}

	var results []types.QueryModalityResponse

	// setup concurrency tools
	var m = sync.Mutex{}
	eg, _ := errgroup.WithContext(ctx)

	// for each series, retrieve instances
	for _, series := range queryModalitySeriesAnswersResponse {
		buf := new(bytes.Buffer)
		err := json.NewEncoder(buf).Encode(map[string]interface{}{
			"Level":     "Instance",
			"LocalAet":  localAet,
			"Normalize": true,
			"Query": map[string]string{
				"StudyInstanceUID":  series.StudyInstanceUID,
				"SeriesInstanceUID": series.SeriesInstanceUID,
			},
			"Timeout": 0,
		})
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/query", o.BaseURL, modalityID), buf)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		var queryResponse types.QueryModalityResponse
		if err := json.NewDecoder(resp.Body).Decode(&queryResponse); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		if len(queryResponse.ID) == 0 {
			continue
		}

		var instancesResponse []map[string]interface{}
		err = o.findQueryAnswers(queryResponse.ID, &instancesResponse)
		if err != nil {
			return nil, err
		}

		if len(instancesResponse) == 0 {
			continue
		}

		// set limit for concurrency
		eg.SetLimit(len(instancesResponse))

		// retrieve each instance individually
		for _, instance := range instancesResponse {
			instanceCopy := instance // create a copy for the closure

			eg.Go(func() error {
				// Extract SOPInstanceUID from instance
				sopInstanceUID, ok := instanceCopy["SOPInstanceUID"].(string)
				if !ok || sopInstanceUID == "" {
					return nil // skip this instance but don't fail the whole operation
				}

				// C-MOVE for this specific instance
				moveBuf := new(bytes.Buffer)
				err := json.NewEncoder(moveBuf).Encode(map[string]interface{}{
					"Level": "Instance",
					"Resources": []map[string]string{
						{
							"StudyInstanceUID":  series.StudyInstanceUID,
							"SeriesInstanceUID": series.SeriesInstanceUID,
							"SOPInstanceUID":    sopInstanceUID,
						},
					},
					"Asynchronous": true,
					"Full":         true,
					"Permissive":   true,
					"Priority":     0,
					"Simplify":     true,
					"Synchronous":  false,
					"TargetAet":    localAet,
					"Timeout":      0,
				})
				if err != nil {
					return err
				}

				moveReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/move", o.BaseURL, modalityID), moveBuf)
				if err != nil {
					return err
				}

				moveResp, err := client.Do(moveReq)
				if err != nil {
					return err
				}
				defer moveResp.Body.Close()

				if moveResp.StatusCode < 200 || moveResp.StatusCode > 299 {
					_, err := io.ReadAll(moveResp.Body)
					if err != nil {
						return err
					}
					return errors.New(apiError.OrthancError)
				}

				var answerResponse types.QueryModalityResponse
				if err := json.NewDecoder(moveResp.Body).Decode(&answerResponse); err != nil {
					return err
				}

				m.Lock()
				results = append(results, answerResponse)
				m.Unlock()

				return nil
			})
		}
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return results, nil
}

// RetrieveDICOMWebInstanceFile retrieve DICOM web instance file
func (o *OrthancAPI) RetrieveDICOMWebInstanceFile(ctx context.Context, studyInstanceUID, seriesInstanceUID, sopInstanceUID string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/dicom-web/studies/%s/series/%s/instances/%s", o.BaseURL, studyInstanceUID, seriesInstanceUID, sopInstanceUID), nil)
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
		return nil, errors.New(apiError.DICOMParseError)
	}

	// get content type and boundary
	contentType := resp.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		log.Println("[dicom-web] error parsing content type", err)
		return nil, errors.New(apiError.DICOMParseError)
	}

	// check if content type is multipart/related
	if strings.HasPrefix(mediaType, "multipart/related") {
		boundary := params["boundary"]
		if len(boundary) == 0 {
			log.Println("[dicom-web] boundary is empty")
			return nil, errors.New(apiError.DICOMParseError)
		}

		mr := multipart.NewReader(resp.Body, boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Println("[dicom-web] error reading part", err)
				return nil, errors.New(apiError.DICOMParseError)
			}

			if strings.HasPrefix(part.Header.Get("Content-Type"), "application/dicom") {
				data, err := io.ReadAll(part)
				if err != nil {
					log.Println("[dicom-web] error reading image data", err)
					return nil, errors.New(apiError.DICOMParseError)
				}

				return data, nil
			}
		}
	}

	log.Println("[dicom-web] no application/dicom part found")
	return nil, errors.New(apiError.DICOMParseError)
}

// RetrieveDICOMWebInstanceMetadata retrieve DICOM web instance metadata
func (o *OrthancAPI) RetrieveDICOMWebInstanceMetadata(ctx context.Context, studyInstanceUID, seriesInstanceUID, sopInstanceUID string) ([]map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/dicom-web/studies/%s/series/%s/instances/%s/metadata", o.BaseURL, studyInstanceUID, seriesInstanceUID, sopInstanceUID), nil)
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

	var instanceMetadata []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&instanceMetadata); err != nil {
		log.Println("Error:", err)
		return nil, err
	}

	return instanceMetadata, nil
}

// StraightDICOMStoreSCU straight DICOM store SCU
func (o *OrthancAPI) StraightDICOMStoreSCU(ctx context.Context, modalityID string, dicomInstances []byte) (types.StraightDICOMStoreSCUResponse, error) {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/store-straight", o.BaseURL, modalityID), bytes.NewReader(dicomInstances))
	if err != nil {
		return types.StraightDICOMStoreSCUResponse{}, err
	}

	// set headers
	req.Header.Set("Content-Type", "application/dicom")

	resp, err := client.Do(req)
	if err != nil {
		return types.StraightDICOMStoreSCUResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return types.StraightDICOMStoreSCUResponse{}, err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return types.StraightDICOMStoreSCUResponse{}, errors.New(apiError.OrthancError)
	}

	var straightDICOMStoreSCUResponse types.StraightDICOMStoreSCUResponse
	if err := json.NewDecoder(resp.Body).Decode(&straightDICOMStoreSCUResponse); err != nil {
		log.Println("Error:", err)
		return types.StraightDICOMStoreSCUResponse{}, err
	}

	return straightDICOMStoreSCUResponse, nil
}

// TriggerDICOMEchoSCU trigger dicom C-ECHO SCU
func (o *OrthancAPI) TriggerDICOMEchoSCU(ctx context.Context, modalityID string) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(map[string]interface{}{
		"CheckFind": false,
		"Timeout":   0,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/echo", o.BaseURL, modalityID), buf)
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

// UpdateDICOMModality create or update dicom modality
func (o *OrthancAPI) UpdateDICOMModality(ctx context.Context, modalityID string, request types.UpdateDICOMModalityRequest) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/modalities/%s", o.BaseURL, modalityID), buf)
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

// UploadDICOMInstances upload dicom instances
func (o *OrthancAPI) UploadDICOMInstances(ctx context.Context, dicomInstances []byte) (types.UploadDICOMInstancesResponse, error) {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/instances", o.BaseURL), bytes.NewReader(dicomInstances))
	if err != nil {
		return types.UploadDICOMInstancesResponse{}, err
	}

	// set headers
	req.Header.Set("Content-Type", "application/dicom")

	resp, err := client.Do(req)
	if err != nil {
		return types.UploadDICOMInstancesResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return types.UploadDICOMInstancesResponse{}, err
		}
		errorMessage := string(response)

		log.Println("Error:", errorMessage)
		return types.UploadDICOMInstancesResponse{}, errors.New(apiError.OrthancError)
	}

	var uploadDICOMInstancesResponse types.UploadDICOMInstancesResponse
	if err := json.NewDecoder(resp.Body).Decode(&uploadDICOMInstancesResponse); err != nil {
		log.Println("Error:", err)
		return types.UploadDICOMInstancesResponse{}, err
	}

	return uploadDICOMInstancesResponse, nil
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

func (o *OrthancAPI) queryModalities(modalityID string, request interface{}, response *types.QueryModalityResponse) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/query", o.BaseURL, modalityID), buf)
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
