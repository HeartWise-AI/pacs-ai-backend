package service

import (
	"context"
	"sort"
	"strings"
	"time"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// recentStudiesTarget is how many studies a dateless search collects before
// it stops looking further back.
const recentStudiesTarget = 500

// recentStudyWindows are the date windows a dateless search walks, newest
// first, as [newest, oldest] days before today. The last window is open-ended:
// everything older than the previous one.
var recentStudyWindows = [][2]int{
	{0, 0},
	{1, 6},
	{7, 29},
	{30, 89},
	{90, 364},
	{365, 1824},
}

const dicomDateLayout = "20060102"

// wantsRecentStudies reports whether a search should return the most recent
// studies first: it has no study date and names no patient, accession or
// study (those searches want the whole history, which is small).
func wantsRecentStudies(data types.FindModalityStudies) bool {
	if strings.TrimSpace(data.StudyDate) != "" {
		return false
	}
	for _, scoped := range []string{
		data.PatientID, data.PatientName, data.PatientBirthDate,
		data.AccessionNumber, data.StudyInstanceUID, data.StudyID,
	} {
		if strings.Trim(strings.TrimSpace(scoped), "*") != "" {
			return false
		}
	}
	return true
}

// findRecentModalityStudies runs the search over date windows, newest first,
// until it has recentStudiesTarget studies or reaches the oldest studies. The
// answers are de-duplicated and sorted newest first. The query ID is the
// first window's.
func (service *OrthancQueryService) findRecentModalityStudies(ctx context.Context, data types.FindModalityStudies, now time.Time) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, string, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	seen := make(map[string]struct{})
	studies := make([]orthancAPITypes.QueryModalityStudyAnswersResponse, 0)
	firstQueryID := ""

	add := func(answers []orthancAPITypes.QueryModalityStudyAnswersResponse, queryID string) {
		if firstQueryID == "" {
			firstQueryID = queryID
		}
		for _, study := range answers {
			key := study.StudyInstanceUID
			if key != "" {
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
			}
			studies = append(studies, study)
		}
	}

	oldestDay := 0
	for _, window := range recentStudyWindows {
		from := today.AddDate(0, 0, -window[1]).Format(dicomDateLayout)
		to := today.AddDate(0, 0, -window[0]).Format(dicomDateLayout)
		answers, queryID, err := service.queryModalityStudies(ctx, data, from+"-"+to)
		if err != nil {
			return nil, "", err
		}
		add(answers, queryID)
		oldestDay = window[1]
		if len(studies) >= recentStudiesTarget {
			return sortStudiesNewestFirst(studies), firstQueryID, nil
		}
	}

	// Everything older than the last window.
	before := today.AddDate(0, 0, -(oldestDay + 1)).Format(dicomDateLayout)
	answers, queryID, err := service.queryModalityStudies(ctx, data, "-"+before)
	if err != nil {
		return nil, "", err
	}
	add(answers, queryID)
	return sortStudiesNewestFirst(studies), firstQueryID, nil
}

// sortStudiesNewestFirst orders studies by study date and time, newest first
// (DICOM DA and TM values sort as text); studies without a date go last.
func sortStudiesNewestFirst(studies []orthancAPITypes.QueryModalityStudyAnswersResponse) []orthancAPITypes.QueryModalityStudyAnswersResponse {
	sort.SliceStable(studies, func(i, j int) bool {
		return strings.TrimSpace(studies[i].StudyDate)+strings.TrimSpace(studies[i].StudyTime) >
			strings.TrimSpace(studies[j].StudyDate)+strings.TrimSpace(studies[j].StudyTime)
	})
	return studies
}
