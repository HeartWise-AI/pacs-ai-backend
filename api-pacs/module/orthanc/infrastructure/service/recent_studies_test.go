package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// fakeStudyPACS answers study C-FINDs from a fixed list of study dates and
// records the StudyDate of every query.
type fakeStudyPACS struct {
	orthancAPITypes.OrthancAPIInterface
	dates   []string
	queries []string
	fail    bool
}

func (pacs *fakeStudyPACS) FindModalityStudies(_ context.Context, _ string, request orthancAPITypes.QueryModalitiesRequest) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, string, error) {
	pacs.queries = append(pacs.queries, request.Query.StudyDate)
	if pacs.fail {
		return nil, "", errors.New("pacs down")
	}
	from, to := "", "99999999"
	if request.Query.StudyDate != "" {
		var parts [2]string
		n, _ := fmt.Sscanf(request.Query.StudyDate, "%8s-%8s", &parts[0], &parts[1])
		if request.Query.StudyDate[0] == '-' {
			to = request.Query.StudyDate[1:]
		} else if n == 2 {
			from, to = parts[0], parts[1]
		}
	}
	answers := []orthancAPITypes.QueryModalityStudyAnswersResponse{}
	for index, date := range pacs.dates {
		if date >= from && date <= to {
			answers = append(answers, orthancAPITypes.QueryModalityStudyAnswersResponse{
				StudyInstanceUID: fmt.Sprintf("1.2.%d", index),
				StudyDate:        date,
				StudyTime:        "120000",
			})
		}
	}
	if len(answers) == 0 {
		return nil, fmt.Sprintf("q%d", len(pacs.queries)), errors.New(apiError.MissingRecord)
	}
	return answers, fmt.Sprintf("q%d", len(pacs.queries)), nil
}

var testNow = time.Date(2026, time.November, 18, 15, 30, 0, 0, time.UTC)

func repeatDate(date string, count int) []string {
	dates := make([]string, count)
	for index := range dates {
		dates[index] = date
	}
	return dates
}

func TestWantsRecentStudiesOnlyForUnscopedDatelessSearches(t *testing.T) {
	require.True(t, wantsRecentStudies(types.FindModalityStudies{ModalitiesInStudy: "XA"}))
	require.True(t, wantsRecentStudies(types.FindModalityStudies{PatientName: "*"}))
	require.False(t, wantsRecentStudies(types.FindModalityStudies{StudyDate: "20261118-20261118"}))
	require.False(t, wantsRecentStudies(types.FindModalityStudies{PatientID: "123"}))
	require.False(t, wantsRecentStudies(types.FindModalityStudies{PatientName: "DOE*"}))
	require.False(t, wantsRecentStudies(types.FindModalityStudies{AccessionNumber: "A1"}))
	require.False(t, wantsRecentStudies(types.FindModalityStudies{StudyInstanceUID: "1.2.3"}))
}

func TestFindRecentModalityStudiesStopsOnceEnoughRecentStudies(t *testing.T) {
	dates := append(repeatDate("20261118", 300), repeatDate("20261115", 250)...)
	dates = append(dates, repeatDate("20100101", 5000)...)
	pacs := &fakeStudyPACS{dates: dates}
	service := &OrthancQueryService{OrthancAPIInterface: pacs}

	studies, queryID, err := service.findRecentModalityStudies(context.Background(), types.FindModalityStudies{ModalitiesInStudy: "XA"}, testNow)
	require.NoError(t, err)
	require.Equal(t, []string{"20261118-20261118", "20261112-20261117"}, pacs.queries)
	require.Len(t, studies, 550)
	require.Equal(t, "q1", queryID)
	require.Equal(t, "20261118", studies[0].StudyDate)
	require.Equal(t, "20261115", studies[len(studies)-1].StudyDate)
}

func TestFindRecentModalityStudiesWalksBackToOlderStudies(t *testing.T) {
	pacs := &fakeStudyPACS{dates: []string{"20261001", "20200101", "20261117", "19990101"}}
	service := &OrthancQueryService{OrthancAPIInterface: pacs}

	studies, _, err := service.findRecentModalityStudies(context.Background(), types.FindModalityStudies{}, testNow)
	require.NoError(t, err)
	require.Equal(t, []string{
		"20261118-20261118",
		"20261112-20261117",
		"20261020-20261111",
		"20260821-20261019",
		"20251119-20260820",
		"20211120-20251118",
		"-20211119",
	}, pacs.queries)
	got := make([]string, len(studies))
	for index, study := range studies {
		got[index] = study.StudyDate
	}
	require.Equal(t, []string{"20261117", "20261001", "20200101", "19990101"}, got)
}

func TestFindRecentModalityStudiesReturnsPACSErrors(t *testing.T) {
	service := &OrthancQueryService{OrthancAPIInterface: &fakeStudyPACS{fail: true}}
	_, _, err := service.findRecentModalityStudies(context.Background(), types.FindModalityStudies{}, testNow)
	require.Error(t, err)
}

func TestFindModalityStudiesKeepsOneQueryForPatientSearches(t *testing.T) {
	pacs := &fakeStudyPACS{dates: []string{"20100101", "20261117", "20200101"}}
	service := &OrthancQueryService{OrthancAPIInterface: pacs}

	studies, _, err := service.FindModalityStudies(context.Background(), types.FindModalityStudies{PatientID: "123"})
	require.NoError(t, err)
	require.Equal(t, []string{""}, pacs.queries)
	require.Equal(t, "20261117", studies[0].StudyDate)
	require.Equal(t, "20100101", studies[2].StudyDate)
}
