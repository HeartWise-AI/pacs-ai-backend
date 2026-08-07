package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/ksuid"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

func ingestionModalitiesFilter(modalities []string) string {
	normalizedModalities := make([]string, 0, len(modalities))
	for _, modality := range modalities {
		modality = strings.TrimSpace(modality)
		if modality == "" {
			continue
		}

		normalizedModalities = append(normalizedModalities, modality)
	}

	slices.Sort(normalizedModalities)
	return strings.Join(normalizedModalities, `\\`)
}

func ingestionCFindCacheKey(tenantID, modalityID, modalitiesInStudy string, queryWindow ingestionQueryWindow) string {
	return strings.Join([]string{
		tenantID,
		modalityID,
		modalitiesInStudy,
		queryWindow.StudyDate,
		queryWindow.StudyTime,
	}, "\x1f")
}

func nullableString(value string) *string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil
	}

	return &trimmedValue
}

func parseNullableInt(value string) (*int, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil, nil
	}

	parsedValue, err := strconv.Atoi(trimmedValue)
	if err != nil {
		return nil, err
	}

	return &parsedValue, nil
}

func normalizeIngestionJobConfig(stabilityMinutes, recentWindowMinutes, missingPollsThreshold uint, studyTimeStart, studyTimeEnd string) (normalizedIngestionJobConfig, error) {
	normalizedStudyTimeStart, err := normalizeOptionalStudyTimeFilterValue(studyTimeStart)
	if err != nil {
		return normalizedIngestionJobConfig{}, errors.New(apiError.InvalidPayload)
	}

	normalizedStudyTimeEnd, err := normalizeOptionalStudyTimeFilterValue(studyTimeEnd)
	if err != nil {
		return normalizedIngestionJobConfig{}, errors.New(apiError.InvalidPayload)
	}

	if normalizedStudyTimeStart != nil && normalizedStudyTimeEnd != nil && *normalizedStudyTimeStart > *normalizedStudyTimeEnd {
		return normalizedIngestionJobConfig{}, errors.New(apiError.InvalidPayload)
	}

	return normalizedIngestionJobConfig{
		RecentWindowMinutes:   resolvedRecentWindowMinutes(recentWindowMinutes),
		StabilityMinutes:      resolvedStabilityMinutes(stabilityMinutes),
		MissingPollsThreshold: resolvedMissingPollsThreshold(missingPollsThreshold),
		StudyTimeStart:        normalizedStudyTimeStart,
		StudyTimeEnd:          normalizedStudyTimeEnd,
	}, nil
}

func resolvedRecentWindowMinutes(value uint) uint {
	if value == 0 {
		if configured := strings.TrimSpace(os.Getenv("INFERENCE_INGESTION_DEFAULT_RECENT_WINDOW_MINUTES")); configured != "" {
			if parsed, err := strconv.ParseUint(configured, 10, 64); err == nil && parsed > 0 {
				return uint(parsed)
			}
			log.Printf("[Ingestion service] invalid INFERENCE_INGESTION_DEFAULT_RECENT_WINDOW_MINUTES=%q, using fallback=%d",
				configured,
				defaultRecentWindowMinutes,
			)
		}
		return defaultRecentWindowMinutes
	}

	return value
}

func resolvedStabilityMinutes(value uint) uint {
	if value == 0 {
		return defaultStabilityMinutes
	}

	return value
}

func resolvedMissingPollsThreshold(value uint) uint {
	if value == 0 {
		return defaultMissingPollsThreshold
	}

	return value
}

func buildIngestionQueryWindows(now time.Time, recentWindowMinutes uint, studyTimeStart, studyTimeEnd *string) ([]ingestionQueryWindow, error) {
	now = now.In(time.Local)
	windowStart := now.Add(-time.Duration(recentWindowMinutes) * time.Minute)
	studyDateStart := windowStart.Format("20060102")
	studyDateEnd := now.Format("20060102")

	normalizedStudyTimeStart, err := normalizeOptionalStudyTimeFilterPointer(studyTimeStart)
	if err != nil {
		return nil, err
	}

	normalizedStudyTimeEnd, err := normalizeOptionalStudyTimeFilterPointer(studyTimeEnd)
	if err != nil {
		return nil, err
	}

	if normalizedStudyTimeStart != nil || normalizedStudyTimeEnd != nil {
		studyDateFilter := studyDateStart
		if studyDateStart != studyDateEnd {
			studyDateFilter = fmt.Sprintf("%s-%s", studyDateStart, studyDateEnd)
		}

		startTime := "000000"
		if normalizedStudyTimeStart != nil {
			startTime = *normalizedStudyTimeStart
		}

		endTime := "235959"
		if normalizedStudyTimeEnd != nil {
			endTime = *normalizedStudyTimeEnd
		}

		return []ingestionQueryWindow{{
			StudyDate: studyDateFilter,
			StudyTime: fmt.Sprintf("%s-%s", startTime, endTime),
		}}, nil
	}

	if studyDateStart == studyDateEnd {
		return []ingestionQueryWindow{{
			StudyDate: studyDateStart,
			StudyTime: fmt.Sprintf("%s-%s", windowStart.Format("150405"), now.Format("150405")),
		}}, nil
	}

	return []ingestionQueryWindow{
		{
			StudyDate: studyDateStart,
			StudyTime: fmt.Sprintf("%s-%s", windowStart.Format("150405"), "235959"),
		},
		{
			StudyDate: studyDateEnd,
			StudyTime: fmt.Sprintf("%s-%s", "000000", now.Format("150405")),
		},
	}, nil
}

func filterStudiesByRecentWindow(studies []orthancAPITypes.QueryModalityStudyAnswersResponse, windowStart, windowEnd time.Time) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, int) {
	windowStart = windowStart.In(time.Local)
	windowEnd = windowEnd.In(time.Local)

	filteredStudies := make([]orthancAPITypes.QueryModalityStudyAnswersResponse, 0, len(studies))
	skippedStudies := 0

	for _, study := range studies {
		studyDateTime, err := parseRemoteStudyDateTime(study.StudyDate, study.StudyTime)
		if err != nil {
			skippedStudies++
			log.Printf("[Ingestion service] cannot parse remote study datetime study_instance_uid=%s study_date=%s study_time=%s err=%v",
				study.StudyInstanceUID,
				study.StudyDate,
				study.StudyTime,
				err,
			)
			continue
		}

		if studyDateTime.Before(windowStart) || studyDateTime.After(windowEnd) {
			skippedStudies++
			continue
		}

		filteredStudies = append(filteredStudies, study)
	}

	return filteredStudies, skippedStudies
}

func parseRemoteStudyDateTime(studyDate, studyTime string) (time.Time, error) {
	trimmedStudyDate := strings.TrimSpace(studyDate)
	if trimmedStudyDate == "" {
		return time.Time{}, errors.New("missing study date")
	}

	normalizedStudyTime := strings.TrimSpace(studyTime)
	normalizedStudyTime = strings.SplitN(normalizedStudyTime, ".", 2)[0]
	normalizedStudyTime = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, normalizedStudyTime)
	if normalizedStudyTime == "" {
		normalizedStudyTime = "000000"
	}
	if len(normalizedStudyTime) < 6 {
		normalizedStudyTime = normalizedStudyTime + strings.Repeat("0", 6-len(normalizedStudyTime))
	}
	if len(normalizedStudyTime) > 6 {
		normalizedStudyTime = normalizedStudyTime[:6]
	}

	studyDateTime, err := time.ParseInLocation("20060102150405", trimmedStudyDate+normalizedStudyTime, time.Local)
	if err != nil {
		return time.Time{}, err
	}

	return studyDateTime, nil
}

func normalizeOptionalStudyTimeFilterPointer(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}

	return normalizeOptionalStudyTimeFilterValue(*value)
}

func normalizeOptionalStudyTimeFilterValue(value string) (*string, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil, nil
	}

	normalizedValue := strings.ReplaceAll(trimmedValue, ":", "")
	switch len(normalizedValue) {
	case 4:
		normalizedValue = fmt.Sprintf("%s00", normalizedValue)
	case 6:
	default:
		return nil, fmt.Errorf("invalid study time format: %s", trimmedValue)
	}

	if _, err := time.Parse("150405", normalizedValue); err != nil {
		return nil, err
	}

	return &normalizedValue, nil
}

func nullableIntLogValue(value *int) string {
	if value == nil {
		return ""
	}

	return strconv.Itoa(*value)
}

func nullableStringLogValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func nullableTimeLogValue(value *time.Time) string {
	if value == nil {
		return ""
	}

	return formatEasternTime(*value)
}

func formatOptionalEasternTime(value time.Time) string {
	if isDisabledScheduleTimestamp(value) {
		return ""
	}

	return formatEasternTime(value)
}

func isDisabledScheduleTimestamp(value time.Time) bool {
	return value.Year() < 1971
}

func formatEasternTime(value time.Time) string {
	return value.In(time.Local).Format(time.RFC3339)
}

func candidateStatusLogValue(status entity.InferenceIngestionCandidateStatus, hasCandidate bool) string {
	if !hasCandidate {
		return ""
	}

	return string(status)
}

func candidateCountsChanged(previousSeriesCount, currentSeriesCount, previousInstanceCount, currentInstanceCount *int) bool {
	return nullableIntChanged(previousSeriesCount, currentSeriesCount) || nullableIntChanged(previousInstanceCount, currentInstanceCount)
}

func nullableIntChanged(previousValue, currentValue *int) bool {
	if previousValue == nil && currentValue == nil {
		return false
	}

	if previousValue == nil || currentValue == nil {
		return true
	}

	return *previousValue != *currentValue
}

func shouldMarkCandidateGrowing(status entity.InferenceIngestionCandidateStatus) bool {
	return status != entity.InferenceIngestionCandidateStatusRetrievalQueued &&
		status != entity.InferenceIngestionCandidateStatusRetrieved &&
		status != entity.InferenceIngestionCandidateStatusFailed
}

func shouldTrackCandidateMissing(status entity.InferenceIngestionCandidateStatus) bool {
	return status != entity.InferenceIngestionCandidateStatusDisappeared &&
		status != entity.InferenceIngestionCandidateStatusRetrievalQueued &&
		status != entity.InferenceIngestionCandidateStatusRetrieved &&
		status != entity.InferenceIngestionCandidateStatusFailed
}

func shouldMarkCandidateStable(status entity.InferenceIngestionCandidateStatus) bool {
	return status == entity.InferenceIngestionCandidateStatusDiscovered ||
		status == entity.InferenceIngestionCandidateStatusGrowing
}

func shouldRetryStudyServiceDispatchHTTPError(err DispatchStudyHTTPError) bool {
	if err.StatusCode == http.StatusTooManyRequests {
		return true
	}

	return err.StatusCode >= http.StatusInternalServerError
}

func boundedRetryAfter(value time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}

	maxDelay := 30 * time.Second
	if value > maxDelay {
		return maxDelay
	}

	return value
}

func extractOrthancJobIDs(responses []orthancAPITypes.QueryModalityResponse) []string {
	jobIDs := make([]string, 0, len(responses))
	seenJobIDs := make(map[string]struct{}, len(responses))

	for _, response := range responses {
		if strings.TrimSpace(response.ID) == "" {
			continue
		}

		if _, seen := seenJobIDs[response.ID]; seen {
			continue
		}

		seenJobIDs[response.ID] = struct{}{}
		jobIDs = append(jobIDs, response.ID)
	}

	return jobIDs
}

func hasOrthancFailure(jobs []orthancAPITypes.GetJobResponse) bool {
	for _, job := range jobs {
		if job.State == string(orthancAPITypes.JobFailure) {
			return true
		}
	}

	return false
}

func hasOrthancSuccess(jobs []orthancAPITypes.GetJobResponse) bool {
	if len(jobs) == 0 {
		return false
	}

	for _, job := range jobs {
		if job.State != string(orthancAPITypes.JobSuccess) {
			return false
		}
	}

	return true
}

func failedOrthancJobs(jobs []orthancAPITypes.GetJobResponse) []orthancAPITypes.GetJobResponse {
	failedJobs := make([]orthancAPITypes.GetJobResponse, 0, len(jobs))

	for _, job := range jobs {
		if job.State == string(orthancAPITypes.JobFailure) {
			failedJobs = append(failedJobs, job)
		}
	}

	return failedJobs
}

func orthancFailureDescription(jobs []orthancAPITypes.GetJobResponse) *string {
	for _, job := range jobs {
		if job.State != string(orthancAPITypes.JobFailure) {
			continue
		}

		description := strings.TrimSpace(job.ErrorDescription)
		if description != "" {
			return &description
		}
	}

	return stringPointer("Orthanc retrieval failed")
}

func marshalOrthancJobs(jobs []orthancAPITypes.GetJobResponse) *string {
	if len(jobs) == 0 {
		return nil
	}

	jobBytes, err := json.Marshal(jobs)
	if err != nil {
		return nil
	}

	jobDetails := string(jobBytes)
	return &jobDetails
}

func coalesceStringPointer(value, fallback *string) *string {
	if value != nil {
		return value
	}

	return fallback
}

func stringPointer(value string) *string {
	return &value
}

func generateID() string {
	return ksuid.New().String()
}
