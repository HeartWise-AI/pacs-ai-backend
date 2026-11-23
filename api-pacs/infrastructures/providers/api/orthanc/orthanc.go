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
	"time"

	"api-pacs/infrastructures/providers/api/orthanc/types"

	apiError "api-pacs/internal/errors"
	"github.com/segmentio/ksuid"
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
			"StudyInstanceUID":               studyInstanceUID,
			"SeriesNumber":                   "",
			"Modality":                       "",
			"NumberOfSeriesRelatedInstances": "",
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

// GetJobInfo get job info. Series moves this process started are retried
// and checked here (see series_move.go), so every caller sees the same state.
func (o *OrthancAPI) GetJobInfo(ctx context.Context, jobID string) (types.GetJobResponse, error) {
	// A finished move, or a background move still waiting for a slot.
	if view, ok := seriesMoves.settledView(jobID); ok {
		return view, nil
	}
	job, err := o.getJobInfo(seriesMoves.current(jobID))
	if err != nil {
		return job, err
	}
	// Callers keep the ID they were given, whichever attempt answers.
	job.ID = jobID

	view := o.moveView(jobID, job)
	seriesMoves.finish(jobID, view)
	return view, nil
}

// moveView is the state reported for a move whose latest attempt is job:
// it retries a failed or incomplete move and checks a successful one.
func (o *OrthancAPI) moveView(jobID string, job types.GetJobResponse) types.GetJobResponse {
	action := seriesMoves.decide(jobID, job)
	if action == moveVerify {
		parts, expected, _ := seriesMoves.parts(jobID)
		local := 0
		for _, part := range parts {
			if part.expected == 0 {
				continue // the PACS gave no count for this series
			}
			count, err := o.countLocalSeriesInstances(part.studyInstanceUID, part.seriesInstanceUID)
			if err != nil {
				// The images are not checked: report the move as still
				// pending, not Orthanc's success, so the reader asks again and
				// the check is retried instead of an incomplete study passing.
				log.Printf("[retrieve] cannot count local images of move job %s: %v", jobID, err)
				return pendingView(job)
			}
			local += min(count, part.expected)
		}
		if seriesMoves.verifiedCount(jobID, local) {
			return job
		}
		log.Printf("[retrieve] move job %s ended with %d of %d images", jobID, local, expected)
		action = seriesMoves.failed(jobID)
		if action == moveReport {
			return incompleteView(job, local, expected)
		}
	}

	switch action {
	case moveWaitRetry:
		return pendingView(job)
	case moveResubmit:
		retry, ok := seriesMoves.retry(jobID)
		if !ok {
			return job
		}
		if retry.background {
			// A background move waits for a background slot again.
			seriesMoves.requeue(jobID)
			log.Printf("[retrieve] series of move job %s queued again after: %s", jobID, job.ErrorDescription)
			return pendingView(job)
		}
		answer, err := o.submitSeriesMove(retry.modalityID, retry.request)
		if err != nil {
			log.Printf("[retrieve] cannot move series again for job %s: %v", jobID, err)
			seriesMoves.resubmitFailed(jobID)
			return pendingView(job)
		}
		seriesMoves.resubmitted(jobID, answer.ID)
		log.Printf("[retrieve] series of move job %s moved again as job %s after: %s", jobID, answer.ID, job.ErrorDescription)
		return pendingView(job)
	}

	return job
}

// countLocalSeriesInstances counts the images of a series in the local store.
// The series resource lists its instances, so the count is not cut by
// Orthanc's LimitFindResults.
func (o *OrthancAPI) countLocalSeriesInstances(studyInstanceUID, seriesInstanceUID string) (int, error) {
	var series []struct {
		Instances []string `json:"Instances"`
	}
	err := o.findLocalResources(map[string]interface{}{
		"Level":  "Series",
		"Expand": true,
		"Query": map[string]string{
			"StudyInstanceUID":  studyInstanceUID,
			"SeriesInstanceUID": seriesInstanceUID,
		},
	}, &series)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range series {
		count += len(item.Instances)
	}
	return count, nil
}

func (o *OrthancAPI) getJobInfo(jobID string) (types.GetJobResponse, error) {
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

// RetrieveModalityStudyBySeries retrieves from a modality what the local
// store lacks of a study: series that are not stored are moved whole, partly
// stored series have only their missing images moved, complete series are
// skipped (see series_move.go). The first series is a job of its own and goes
// first; the other series share a few jobs (batchMoves). interactive is true
// when someone is waiting for the study in the viewer.
// Returns DuplicateRecord when nothing is missing.
func (o *OrthancAPI) RetrieveModalityStudyBySeries(ctx context.Context, modalityID, localAet, studyInstanceUID string, interactive bool) ([]types.QueryModalityResponse, error) {
	remote, err := o.FindModalitySeriesByStudy(ctx, modalityID, localAet, studyInstanceUID)
	if err != nil {
		return nil, err
	}
	local, err := o.localSeriesInventory(studyInstanceUID)
	if err != nil {
		return nil, err
	}

	jobs := batchMoves(planStudyMoves(orderSeriesForMove(remote), local))
	results := make([]types.QueryModalityResponse, 0, len(jobs))

	// Submitting only queues the jobs in Orthanc, which runs them a few at a
	// time, highest priority first: the first series' job comes first.
	for index, job := range jobs {
		priority := seriesMovePriority(interactive, index, len(jobs))
		first := job[0]
		request := seriesBatchMoveRequest(job, localAet, priority)
		if first.kind == moveMissingInstances {
			// A job of partly stored series: move only their missing images.
			var items []seriesInstances
			var moved []plannedMove
			for _, move := range job {
				missing, err := o.missingSeriesInstances(modalityID, localAet, move)
				if err != nil {
					return nil, err
				}
				if len(missing) > 0 {
					items = append(items, seriesInstances{series: move.series, sopInstanceUIDs: missing})
					moved = append(moved, move)
				}
			}
			if len(items) == 0 {
				continue
			}
			job, first = moved, moved[0]
			request = instancesBatchMoveRequest(items, localAet, priority)
		}

		series := make([]types.QueryModalitySeriesAnswersResponse, 0, len(job))
		for _, move := range job {
			series = append(series, move.series)
		}

		var answer types.QueryModalityResponse
		if interactive {
			answer, err = o.submitSeriesMove(modalityID, request)
			if err != nil {
				return nil, err
			}
			seriesMoves.track(answer.ID, modalityID, request, series...)
		} else {
			// Background moves wait in api-pacs for a background slot; the
			// caller keeps this ID (see move_supervisor.go).
			id := queuedMoveIDPrefix + ksuid.New().String()
			seriesMoves.enqueue(id, modalityID, request, series...)
			answer = types.QueryModalityResponse{ID: id, Path: "/jobs/" + id}
		}
		answer.SeriesInstanceUID = first.series.SeriesInstanceUID
		answer.SeriesNumber = strings.TrimSpace(first.series.SeriesNumber)
		answer.Order = len(results)
		results = append(results, answer)
	}

	if len(results) == 0 {
		// Every series of the PACS is complete locally.
		return nil, errors.New(apiError.DuplicateRecord)
	}
	if !interactive {
		if err := o.DispatchBackgroundMoves(); err != nil {
			// The supervisor sends them on its next pass.
			log.Printf("[retrieve] cannot send background moves of study now: %v", err)
		}
	}

	return results, nil
}

// IDs of background moves that wait in api-pacs before Orthanc has a job.
const queuedMoveIDPrefix = "pacsai-move-"

// localSeriesInventory lists the stored series of a study with their image counts.
func (o *OrthancAPI) localSeriesInventory(studyInstanceUID string) (map[string]localSeries, error) {
	var series []struct {
		ID            string   `json:"ID"`
		Instances     []string `json:"Instances"`
		MainDicomTags struct {
			SeriesInstanceUID string `json:"SeriesInstanceUID"`
		} `json:"MainDicomTags"`
	}
	err := o.findLocalResources(map[string]interface{}{
		"Level":  "Series",
		"Expand": true,
		"Query":  map[string]string{"StudyInstanceUID": studyInstanceUID},
	}, &series)
	if err != nil {
		return nil, err
	}
	inventory := make(map[string]localSeries, len(series))
	for _, item := range series {
		inventory[item.MainDicomTags.SeriesInstanceUID] = localSeries{orthancID: item.ID, instances: len(item.Instances)}
	}
	return inventory, nil
}

// missingSeriesInstances lists the images of a partly stored series that the
// PACS has and the local store lacks.
func (o *OrthancAPI) missingSeriesInstances(modalityID, localAet string, move plannedMove) ([]string, error) {
	var query types.QueryModalityResponse
	err := o.queryModalities(modalityID, map[string]interface{}{
		"Level":     "Instance",
		"LocalAet":  localAet,
		"Normalize": true,
		"Query": map[string]string{
			"StudyInstanceUID":  move.series.StudyInstanceUID,
			"SeriesInstanceUID": move.series.SeriesInstanceUID,
			"SOPInstanceUID":    "",
		},
		"Timeout": 0,
	}, &query)
	if err != nil {
		return nil, err
	}
	var remoteAnswers []struct {
		SOPInstanceUID string `json:"SOPInstanceUID"`
	}
	if query.ID != "" {
		if err := o.findQueryAnswers(query.ID, &remoteAnswers); err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/series/%s/instances", o.BaseURL, move.local.orthancID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, errors.New(apiError.OrthancError)
	}
	var localAnswers []struct {
		MainDicomTags struct {
			SOPInstanceUID string `json:"SOPInstanceUID"`
		} `json:"MainDicomTags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&localAnswers); err != nil {
		return nil, err
	}

	remote := make([]string, 0, len(remoteAnswers))
	for _, answer := range remoteAnswers {
		remote = append(remote, answer.SOPInstanceUID)
	}
	local := make([]string, 0, len(localAnswers))
	for _, answer := range localAnswers {
		local = append(local, answer.MainDicomTags.SOPInstanceUID)
	}
	return missingInstances(remote, local), nil
}

func (o *OrthancAPI) submitSeriesMove(modalityID string, request map[string]interface{}) (types.QueryModalityResponse, error) {
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(request); err != nil {
		return types.QueryModalityResponse{}, err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/modalities/%s/move", o.BaseURL, modalityID), buf)
	if err != nil {
		return types.QueryModalityResponse{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return types.QueryModalityResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, _ := io.ReadAll(resp.Body)
		log.Println("Error:", string(response))
		return types.QueryModalityResponse{}, errors.New(apiError.OrthancError)
	}

	var answer types.QueryModalityResponse
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return types.QueryModalityResponse{}, err
	}
	return answer, nil
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
