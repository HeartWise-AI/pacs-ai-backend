package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

type predictionsRepositoryFake struct {
	examQueries        []repositoryTypes.ListPredictionExams
	executionQueries   []repositoryTypes.ListPredictionExecutions
	historyQueries     []repositoryTypes.ListPredictionModelHistory
	launcherTenants    []string
	pages              []repositoryTypes.PredictionExamPage
	executions         []repositoryTypes.PredictionExecutionRow
	history            []repositoryTypes.PredictionExecutionRow
	launchers          []repositoryTypes.PredictionLauncherRow
	reportCounts       []repositoryTypes.ReturnedReportCountRow
	reportCountQueries []repositoryTypes.CountReturnedReports
	err                error
}

func (fake *predictionsRepositoryFake) ListPredictionExams(_ context.Context, data repositoryTypes.ListPredictionExams) (repositoryTypes.PredictionExamPage, error) {
	fake.examQueries = append(fake.examQueries, data)
	if fake.err != nil {
		return repositoryTypes.PredictionExamPage{}, fake.err
	}
	if len(fake.pages) == 0 {
		return repositoryTypes.PredictionExamPage{Exams: []repositoryTypes.PredictionExamRow{}}, nil
	}
	page := fake.pages[0]
	fake.pages = fake.pages[1:]
	return page, nil
}

func (fake *predictionsRepositoryFake) ListPredictionExecutions(_ context.Context, data repositoryTypes.ListPredictionExecutions) ([]repositoryTypes.PredictionExecutionRow, error) {
	fake.executionQueries = append(fake.executionQueries, data)
	rows := []repositoryTypes.PredictionExecutionRow{}
	wanted := map[string]bool{}
	for _, uid := range data.StudyInstanceUIDs {
		wanted[uid] = true
	}
	for _, row := range fake.executions {
		if wanted[row.StudyInstanceUID] {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (fake *predictionsRepositoryFake) ListPredictionModelHistory(_ context.Context, data repositoryTypes.ListPredictionModelHistory) ([]repositoryTypes.PredictionExecutionRow, error) {
	fake.historyQueries = append(fake.historyQueries, data)
	return fake.history, nil
}

func (fake *predictionsRepositoryFake) ListPredictionLaunchers(_ context.Context, tenantID string, _ []string) ([]repositoryTypes.PredictionLauncherRow, error) {
	fake.launcherTenants = append(fake.launcherTenants, tenantID)
	return fake.launchers, nil
}

func (fake *predictionsRepositoryFake) CountReturnedReports(_ context.Context, data repositoryTypes.CountReturnedReports) ([]repositoryTypes.ReturnedReportCountRow, error) {
	fake.reportCountQueries = append(fake.reportCountQueries, data)
	if fake.err != nil {
		return nil, fake.err
	}
	return fake.reportCounts, nil
}

type predictionsUsersFake struct {
	users []userTypes.GetTenantUser
	err   error
	calls int
}

func (fake *predictionsUsersFake) GetTenantUsers(context.Context, string) ([]userTypes.GetTenantUser, error) {
	fake.calls++
	return fake.users, fake.err
}

func strPointer(value string) *string { return &value }

var predictionsTestNow = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func pinnedNow() time.Time { return predictionsTestNow }

func basePredictionsRequest(view types.PredictionsView, role string) types.ListPredictions {
	return types.ListPredictions{TenantID: "tenant-a", UserID: "user-a", Role: role, View: view}
}

func TestPredictionsViewsAreAuthorizedPerRole(t *testing.T) {
	cases := []struct {
		name      string
		request   types.ListPredictions
		err       string
		scope     repositoryTypes.PredictionsScope
		requester string
	}{
		{name: "default view is mine", request: basePredictionsRequest("", "USER"),
			scope: repositoryTypes.PredictionsScopeRequestedBy, requester: "user-a"},
		{name: "mine is always the authenticated user", request: basePredictionsRequest(types.PredictionsViewMine, "ADMIN"),
			scope: repositoryTypes.PredictionsScopeRequestedBy, requester: "user-a"},
		{name: "automatic for a user", request: basePredictionsRequest(types.PredictionsViewAutomatic, "USER"),
			scope: repositoryTypes.PredictionsScopeAutomatic},
		{name: "all is forbidden to a user", request: basePredictionsRequest(types.PredictionsViewAll, "USER"),
			err: apiError.ForbiddenAccess},
		{name: "all is forbidden without a role", request: basePredictionsRequest(types.PredictionsViewAll, ""),
			err: apiError.ForbiddenAccess},
		{name: "all for an admin", request: basePredictionsRequest(types.PredictionsViewAll, "ADMIN"),
			scope: repositoryTypes.PredictionsScopeAll},
		{name: "all for an owner", request: basePredictionsRequest(types.PredictionsViewAll, "OWNER"),
			scope: repositoryTypes.PredictionsScopeAll},
		{name: "unknown view", request: basePredictionsRequest("everyone", "OWNER"), err: apiError.InvalidPayload},
		{name: "no tenant", request: types.ListPredictions{UserID: "user-a", Role: "OWNER"}, err: apiError.UnauthorizedAccess},
		{name: "no user", request: types.ListPredictions{TenantID: "tenant-a", Role: "OWNER"}, err: apiError.UnauthorizedAccess},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &predictionsRepositoryFake{}
			service := InferencePredictionsService{Now: pinnedNow, Repository: repository}
			page, err := service.ListPredictions(context.Background(), testCase.request)
			if testCase.err != "" {
				require.EqualError(t, err, testCase.err)
				require.Empty(t, repository.examQueries, "a refused view never reaches the database")
				return
			}
			require.NoError(t, err)
			require.Len(t, repository.examQueries, 1)
			query := repository.examQueries[0]
			require.Equal(t, "tenant-a", query.TenantID)
			require.Equal(t, testCase.scope, query.Scope)
			require.Equal(t, testCase.requester, query.RequestedByUserID)
			require.Equal(t, testCase.request.Role == "ADMIN" || testCase.request.Role == "OWNER", page.CanViewAll)
			require.NotNil(t, page.Exams)
		})
	}
}

func TestPredictionsLaunchedByFilterIsOnlyForTheAllView(t *testing.T) {
	cases := []struct {
		launchedBy string
		scope      repositoryTypes.PredictionsScope
		requester  string
	}{
		{"", repositoryTypes.PredictionsScopeAll, ""},
		{"any", repositoryTypes.PredictionsScopeAll, ""},
		{"automatic", repositoryTypes.PredictionsScopeAutomatic, ""},
		{"people", repositoryTypes.PredictionsScopePeople, ""},
		{"user:user-b", repositoryTypes.PredictionsScopeRequestedBy, "user-b"},
	}
	for _, testCase := range cases {
		repository := &predictionsRepositoryFake{}
		service := InferencePredictionsService{Now: pinnedNow, Repository: repository}
		request := basePredictionsRequest(types.PredictionsViewAll, "ADMIN")
		request.LaunchedBy = testCase.launchedBy
		_, err := service.ListPredictions(context.Background(), request)
		require.NoError(t, err, testCase.launchedBy)
		require.Equal(t, testCase.scope, repository.examQueries[0].Scope, testCase.launchedBy)
		require.Equal(t, testCase.requester, repository.examQueries[0].RequestedByUserID, testCase.launchedBy)
	}

	for _, invalid := range []string{"user:", "someone", "user: "} {
		request := basePredictionsRequest(types.PredictionsViewAll, "ADMIN")
		request.LaunchedBy = invalid
		_, err := (&InferencePredictionsService{Now: pinnedNow, Repository: &predictionsRepositoryFake{}}).ListPredictions(context.Background(), request)
		require.EqualError(t, err, apiError.InvalidPayload, invalid)
	}
	// A regular user cannot read someone else's runs through "Launched by".
	for _, view := range []types.PredictionsView{types.PredictionsViewMine, types.PredictionsViewAutomatic} {
		request := basePredictionsRequest(view, "USER")
		request.LaunchedBy = "user:user-b"
		repository := &predictionsRepositoryFake{}
		_, err := (&InferencePredictionsService{Now: pinnedNow, Repository: repository}).ListPredictions(context.Background(), request)
		require.EqualError(t, err, apiError.InvalidPayload)
		require.Empty(t, repository.examQueries)
	}
}

func TestPredictionsFiltersAreValidatedAndPassedThrough(t *testing.T) {
	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	to := from.Add(7 * 24 * time.Hour)
	repository := &predictionsRepositoryFake{}
	service := InferencePredictionsService{Now: pinnedNow, Repository: repository}
	request := basePredictionsRequest(types.PredictionsViewAutomatic, "USER")
	request.Search, request.Model, request.Status, request.Modality = "  ACC-1 ", "DeepCORO-JEPA", "attention", "XA"
	request.From, request.To, request.Limit, request.Offset = &from, &to, 50, 100
	_, err := service.ListPredictions(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, repositoryTypes.ListPredictionExams{
		TenantID: "tenant-a", Scope: repositoryTypes.PredictionsScopeAutomatic, ModelName: "DeepCORO-JEPA",
		Status: repositoryTypes.PredictionsExamStatusAttention, Modality: "XA", Search: "ACC-1",
		From: &from, To: &to, StalledBefore: predictionsTestNow.Add(-24 * time.Hour), Limit: 50, Offset: 100,
	}, repository.examQueries[0])

	for status, expected := range map[string]repositoryTypes.PredictionsExamStatus{
		"": repositoryTypes.PredictionsExamStatusAny, "any": repositoryTypes.PredictionsExamStatusAny,
		"active": repositoryTypes.PredictionsExamStatusActive, "completed": repositoryTypes.PredictionsExamStatusCompleted,
	} {
		repository := &predictionsRepositoryFake{}
		request := basePredictionsRequest(types.PredictionsViewMine, "USER")
		request.Status = status
		_, err := (&InferencePredictionsService{Now: pinnedNow, Repository: repository}).ListPredictions(context.Background(), request)
		require.NoError(t, err)
		require.Equal(t, expected, repository.examQueries[0].Status)
		require.Equal(t, defaultPredictionsPageSize, repository.examQueries[0].Limit)
	}

	invalid := []func(*types.ListPredictions){
		func(request *types.ListPredictions) { request.Status = "broken" },
		func(request *types.ListPredictions) { request.Modality = "X A" },
		func(request *types.ListPredictions) { request.Modality = "XA'--" },
		func(request *types.ListPredictions) { request.Search = strings.Repeat("a", 129) },
		func(request *types.ListPredictions) { request.Limit = -1 },
		func(request *types.ListPredictions) { request.Offset = -1 },
		func(request *types.ListPredictions) { request.From, request.To = &to, &from },
	}
	for index, mutate := range invalid {
		request := basePredictionsRequest(types.PredictionsViewMine, "USER")
		mutate(&request)
		_, err := (&InferencePredictionsService{Now: pinnedNow, Repository: &predictionsRepositoryFake{}}).ListPredictions(context.Background(), request)
		require.EqualError(t, err, apiError.InvalidPayload, "case %d", index)
	}
	request = basePredictionsRequest(types.PredictionsViewMine, "USER")
	request.Limit = 101
	_, err = (&InferencePredictionsService{Now: pinnedNow, Repository: &predictionsRepositoryFake{}}).ListPredictions(context.Background(), request)
	require.EqualError(t, err, apiError.MaximumLimitReached)
}

func predictionsFixture() *predictionsRepositoryFake {
	now := predictionsTestNow
	started := now.Add(-2 * time.Minute)
	completed := now.Add(-time.Minute)
	growing := entity.InferenceIngestionCandidateStatusGrowing
	retrieved := entity.InferenceIngestionCandidateStatusRetrieved
	skip := entity.InferenceIngestionProcessingJobSkipReasonRequiredSeriesMissing
	fake := &predictionsRepositoryFake{
		pages: []repositoryTypes.PredictionExamPage{{Total: 3, Exams: []repositoryTypes.PredictionExamRow{
			{StudyInstanceUID: "1.1", LastActivityAt: now, PatientID: strPointer("MRN-1"), Total: 3},
			{StudyInstanceUID: "1.2", LastActivityAt: now.Add(-time.Hour), Total: 3},
		}}},
		executions: []repositoryTypes.PredictionExecutionRow{
			{StudyInstanceUID: "1.1", RunID: "run-1", ExecutionID: "ex-1", RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
				RunPhase: entity.InferenceIngestionProcessingRunPhaseTerminal, ModelName: "DeepCORO-JEPA",
				Status: entity.InferenceIngestionProcessingJobStatusCompleted, StartedAt: &started, CompletedAt: &completed,
				CandidateStatus: &retrieved, EarlierRuns: 2},
			{StudyInstanceUID: "1.1", RunID: "run-2", ExecutionID: "ex-2", RunTrigger: entity.InferenceIngestionProcessingRunTriggerViewerRequest,
				RunPhase: entity.InferenceIngestionProcessingRunPhaseProcessing, RequestedByUserID: strPointer("user-b"),
				ModelName: "CathEF-CLIP", Status: entity.InferenceIngestionProcessingJobStatusPending, CandidateStatus: &growing},
			{StudyInstanceUID: "1.1", RunID: "run-2", ExecutionID: "ex-3", RunTrigger: entity.InferenceIngestionProcessingRunTriggerViewerRequest,
				RunPhase: entity.InferenceIngestionProcessingRunPhaseProcessing, RequestedByUserID: strPointer("user-a"),
				ModelName: "DeepRV-CLIP", Status: entity.InferenceIngestionProcessingJobStatusQueued,
				StudyServiceJobID: strPointer("ss-1"), CandidateStatus: &retrieved},
			{StudyInstanceUID: "1.2", RunID: "run-3", ExecutionID: "ex-4", RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess,
				RunPhase: entity.InferenceIngestionProcessingRunPhaseTerminal, RequestedByUserID: strPointer("user-a"),
				ModelName: "DeepCORO-SYNTAX", Status: entity.InferenceIngestionProcessingJobStatusSkipped,
				SkipReasonCode: &skip, SkipReasonMessage: strPointer("no left coronary video")},
			{StudyInstanceUID: "1.2", RunID: "run-3", ExecutionID: "ex-5", RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess,
				RunPhase: entity.InferenceIngestionProcessingRunPhaseTerminal, RequestedByUserID: strPointer("user-a"),
				ModelName: "DeepCORO-CTO", Status: entity.InferenceIngestionProcessingJobStatusFailed,
				ErrorMessage: strPointer("=HYPERLINK(\"http://x\")")},
		},
		launchers: []repositoryTypes.PredictionLauncherRow{
			{StudyInstanceUID: "1.1", RunTrigger: entity.InferenceIngestionProcessingRunTriggerViewerRequest, RequestedByUserID: strPointer("user-b"), Runs: 2, FirstAt: now.Add(-30 * time.Minute)},
			{StudyInstanceUID: "1.1", RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto, Runs: 1, FirstAt: now.Add(-3 * time.Hour)},
			{StudyInstanceUID: "1.1", RunTrigger: entity.InferenceIngestionProcessingRunTriggerLegacyImport, Runs: 1, FirstAt: now.Add(-4 * time.Hour)},
			{StudyInstanceUID: "1.2", RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess, RequestedByUserID: strPointer("user-a"), Runs: 1, FirstAt: now.Add(-time.Hour)},
		},
	}
	for index := range fake.executions {
		fake.executions[index].RunCreatedAt = now.Add(-30 * time.Minute)
	}
	return fake
}

func TestListPredictionsBuildsExamRowsWithStatesNamesAndLaunchers(t *testing.T) {
	repository := predictionsFixture()
	users := &predictionsUsersFake{users: []userTypes.GetTenantUser{
		{ID: "user-a", TenantID: "tenant-a", Name: "Dr A"},
		{ID: "user-b", TenantID: "tenant-a", Name: "Dr B"},
		{ID: "user-x", TenantID: "tenant-b", Name: "Other tenant"},
	}}
	service := InferencePredictionsService{Now: pinnedNow, Repository: repository, Users: users}

	page, err := service.ListPredictions(context.Background(), basePredictionsRequest(types.PredictionsViewMine, "USER"))
	require.NoError(t, err)
	require.Equal(t, 1, users.calls, "names are looked up once per page")
	require.Equal(t, 3, page.Total)
	require.True(t, page.HasMore)
	require.Len(t, page.Exams, 2)
	require.Equal(t, []string{"1.1", "1.2"}, repository.executionQueries[0].StudyInstanceUIDs)
	require.True(t, repository.executionQueries[0].LatestOnly)
	require.Equal(t, "tenant-a", repository.executionQueries[0].TenantID)
	require.Equal(t, []string{"tenant-a"}, repository.launcherTenants)

	exam := page.Exams[0]
	require.Equal(t, "MRN-1", *exam.PatientID)
	require.True(t, exam.Active)
	require.Equal(t, types.PredictionCounts{Total: 3, Completed: 1, Waiting: 1, Queued: 1}, exam.Counts)
	require.Equal(t, predictionsTestNow.Add(-24*time.Hour), repository.examQueries[0].StalledBefore)
	jepa, cathEF, deepRV := exam.Models[0], exam.Models[1], exam.Models[2]
	require.Equal(t, types.PredictionStateCompleted, jepa.State)
	require.Equal(t, 2, jepa.EarlierRuns)
	require.InDelta(t, 60, *jepa.DurationSeconds, 0.001)
	require.Nil(t, jepa.RequestedByUserID)
	require.False(t, jepa.Cancellable)
	require.Equal(t, types.PredictionStateWaiting, cathEF.State, "pending while the exam is still growing")
	require.True(t, cathEF.Cancellable)
	require.Equal(t, "Dr B", *cathEF.RequestedByName)
	require.False(t, cathEF.RequestedByYou)
	require.Equal(t, types.PredictionStateQueued, deepRV.State)
	require.False(t, deepRV.Cancellable, "queued at study-service already")
	require.True(t, deepRV.RequestedByYou)

	// Automatic triggers are merged; launchers are ordered by first run.
	require.Len(t, exam.Launchers, 2)
	require.Equal(t, "automatic", exam.Launchers[0].Kind)
	require.Equal(t, 2, exam.Launchers[0].Runs)
	require.Equal(t, "user", exam.Launchers[1].Kind)
	require.Equal(t, "Dr B", *exam.Launchers[1].Name)

	second := page.Exams[1]
	require.False(t, second.Active)
	require.Equal(t, types.PredictionCounts{Total: 2, Skipped: 1, Failed: 1}, second.Counts)
	require.Equal(t, entity.InferenceIngestionProcessingJobSkipReasonRequiredSeriesMissing, second.Models[0].SkipReason.Code)
	require.True(t, second.Launchers[0].You)
}

func TestListPredictionsLeavesNamesUnknownWhenTheLookupFails(t *testing.T) {
	service := InferencePredictionsService{Now: pinnedNow, Repository: predictionsFixture(), Users: &predictionsUsersFake{err: errors.New("firestore down")}}
	page, err := service.ListPredictions(context.Background(), basePredictionsRequest(types.PredictionsViewMine, "USER"))
	require.NoError(t, err)
	require.Nil(t, page.Exams[0].Models[1].RequestedByName)
	require.Equal(t, "user-b", *page.Exams[0].Models[1].RequestedByUserID)
}

func TestListPredictionsPropagatesRepositoryErrors(t *testing.T) {
	service := InferencePredictionsService{Now: pinnedNow, Repository: &predictionsRepositoryFake{err: errors.New(apiError.DatabaseError)}}
	_, err := service.ListPredictions(context.Background(), basePredictionsRequest(types.PredictionsViewMine, "USER"))
	require.EqualError(t, err, apiError.DatabaseError)
}

func TestPredictionModelHistoryPagesNewestFirst(t *testing.T) {
	repository := predictionsFixture()
	repository.history = repository.executions[:3]
	service := InferencePredictionsService{Now: pinnedNow, Repository: repository}
	page, err := service.GetPredictionModelHistory(context.Background(), types.GetPredictionModelHistory{
		TenantID: "tenant-a", UserID: "user-a", StudyInstanceUID: " 1.1 ", Model: "DeepCORO-JEPA", Limit: 2,
	})
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Len(t, page.Executions, 2)
	require.Equal(t, repositoryTypes.ListPredictionModelHistory{
		TenantID: "tenant-a", StudyInstanceUID: "1.1", ModelName: "DeepCORO-JEPA", Limit: 2,
	}, repository.historyQueries[0])

	for _, request := range []types.GetPredictionModelHistory{
		{TenantID: "tenant-a", UserID: "user-a", StudyInstanceUID: "1.1"},
		{TenantID: "tenant-a", UserID: "user-a", Model: "m"},
		{TenantID: "tenant-a", UserID: "user-a", StudyInstanceUID: "1.1", Model: "m", Limit: -1},
	} {
		_, err := service.GetPredictionModelHistory(context.Background(), request)
		require.EqualError(t, err, apiError.InvalidPayload)
	}
	_, err = service.GetPredictionModelHistory(context.Background(), types.GetPredictionModelHistory{StudyInstanceUID: "1.1", Model: "m"})
	require.EqualError(t, err, apiError.UnauthorizedAccess)
	_, err = service.GetPredictionModelHistory(context.Background(), types.GetPredictionModelHistory{
		TenantID: "tenant-a", UserID: "user-a", StudyInstanceUID: "1.1", Model: "m", Limit: 51,
	})
	require.EqualError(t, err, apiError.MaximumLimitReached)
}

func TestCSVSafeCellNeutralisesFormulas(t *testing.T) {
	for input, expected := range map[string]string{
		"=1+1":         "'=1+1",
		"+33 1 23":     "'+33 1 23",
		"-2":           "'-2",
		"@SUM(A1)":     "'@SUM(A1)",
		"\tcmd":        "'\tcmd",
		"\rcmd":        "'\rcmd",
		"Dr A":         "Dr A",
		"":             "",
		"a=b":          "a=b",
		"1.2.840.1234": "1.2.840.1234",
	} {
		require.Equal(t, expected, CSVSafeCell(input), input)
	}
}

func TestExportPredictionsWritesOneEscapedLinePerRun(t *testing.T) {
	repository := predictionsFixture()
	repository.pages[0].Total = 2
	users := &predictionsUsersFake{users: []userTypes.GetTenantUser{{ID: "user-a", TenantID: "tenant-a", Name: "=cmd|'/C calc'!A0"}}}
	service := InferencePredictionsService{Now: pinnedNow, Repository: repository, Users: users}
	var buffer bytes.Buffer
	truncated := true
	request := basePredictionsRequest(types.PredictionsViewAll, "OWNER")
	request.LaunchedBy = "people"
	summary, err := service.ExportPredictions(context.Background(), request, func(value bool) io.Writer {
		truncated = value
		return &buffer
	})
	require.NoError(t, err)
	require.False(t, truncated)
	require.Equal(t, 5, summary.Rows)
	require.Equal(t, repositoryTypes.PredictionsScopePeople, repository.executionQueries[0].LaunchedBy)
	require.False(t, repository.executionQueries[0].LatestOnly)

	lines, err := csv.NewReader(&buffer).ReadAll()
	require.NoError(t, err)
	require.Len(t, lines, 6)
	require.Equal(t, PredictionsExportHeader, lines[0])
	require.Equal(t, "1.1", lines[1][1])
	require.Equal(t, "MRN-1", lines[1][3])
	require.Equal(t, "Automatic", lines[1][9])
	require.Equal(t, "completed", lines[1][10])
	require.Equal(t, "60.0", lines[1][11])
	require.Equal(t, "", lines[1][4], "patient names are not stored by the gateway")
	require.Equal(t, "user-b", lines[2][9], "an unknown name falls back to the user ID")
	require.Equal(t, "'=cmd|'/C calc'!A0", lines[3][9], "formula in a user name is neutralised")
	require.Equal(t, "REQUIRED_SERIES_MISSING: no left coronary video", lines[4][13])
	require.Equal(t, "'=HYPERLINK(\"http://x\")", lines[5][13], "formula in an error message is neutralised")
	require.Equal(t, "Work list reprocess", lines[5][8])
	require.Equal(t, "ex-5", lines[5][15])
}

func TestExportPredictionsRefusesBeforeWritingAndReportsTruncation(t *testing.T) {
	service := InferencePredictionsService{Now: pinnedNow, Repository: &predictionsRepositoryFake{}}
	started := false
	_, err := service.ExportPredictions(context.Background(), basePredictionsRequest(types.PredictionsViewAll, "USER"),
		func(bool) io.Writer { started = true; return io.Discard })
	require.EqualError(t, err, apiError.ForbiddenAccess)
	require.False(t, started)

	repository := &predictionsRepositoryFake{pages: []repositoryTypes.PredictionExamPage{{
		Total: MaximumPredictionsExportExams + 1, Exams: []repositoryTypes.PredictionExamRow{},
	}}}
	truncated := false
	_, err = (&InferencePredictionsService{Now: pinnedNow, Repository: repository}).ExportPredictions(context.Background(),
		basePredictionsRequest(types.PredictionsViewMine, "USER"),
		func(value bool) io.Writer { truncated = value; return io.Discard })
	require.NoError(t, err)
	require.True(t, truncated)
	require.Equal(t, predictionsExportBatch, repository.examQueries[0].Limit)
	require.Equal(t, repositoryTypes.PredictionsScopeRequestedBy, repository.examQueries[0].Scope)
}

func TestExportPredictionsReadsEveryPage(t *testing.T) {
	first := make([]repositoryTypes.PredictionExamRow, predictionsExportBatch)
	for index := range first {
		first[index] = repositoryTypes.PredictionExamRow{StudyInstanceUID: "1.1"}
	}
	repository := &predictionsRepositoryFake{pages: []repositoryTypes.PredictionExamPage{
		{Total: predictionsExportBatch + 1, Exams: first},
		{Total: predictionsExportBatch + 1, Exams: []repositoryTypes.PredictionExamRow{{StudyInstanceUID: "1.2"}}},
	}}
	_, err := (&InferencePredictionsService{Now: pinnedNow, Repository: repository}).ExportPredictions(context.Background(),
		basePredictionsRequest(types.PredictionsViewAutomatic, "USER"), func(bool) io.Writer { return io.Discard })
	require.NoError(t, err)
	require.Len(t, repository.examQueries, 2)
	require.Equal(t, predictionsExportBatch, repository.examQueries[1].Offset)
}

func stalledPredictionRow(status entity.InferenceIngestionProcessingJobStatus, studyServiceJobID *string, age time.Duration) repositoryTypes.PredictionExecutionRow {
	growing := entity.InferenceIngestionCandidateStatusGrowing
	return repositoryTypes.PredictionExecutionRow{
		StudyInstanceUID: "2.1", RunID: "run-s", ExecutionID: "ex-s", ModelName: "DeepCORO-JEPA",
		RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
		RunPhase:   entity.InferenceIngestionProcessingRunPhaseQueued,
		Status:     status, StudyServiceJobID: studyServiceJobID, CandidateStatus: &growing,
		RunCreatedAt: predictionsTestNow.Add(-age),
	}
}

func TestPredictionExecutionStateMarksExecutionsThatNeverRanAsStalled(t *testing.T) {
	stalledBefore := predictionsTestNow.Add(-24 * time.Hour)
	pending := entity.InferenceIngestionProcessingJobStatusPending
	queued := entity.InferenceIngestionProcessingJobStatusQueued
	cases := []struct {
		name     string
		row      repositoryTypes.PredictionExecutionRow
		expected types.PredictionState
	}{
		{"pending older than the limit", stalledPredictionRow(pending, nil, 25*time.Hour), types.PredictionStateStalled},
		{"queued older than the limit", stalledPredictionRow(queued, nil, 25*time.Hour), types.PredictionStateStalled},
		{"blank study-service job id counts as none", stalledPredictionRow(queued, strPointer("  "), 25*time.Hour), types.PredictionStateStalled},
		{"one nanosecond past the limit", stalledPredictionRow(pending, nil, 24*time.Hour+time.Nanosecond), types.PredictionStateStalled},
		{"exactly at the limit is not stalled yet", stalledPredictionRow(pending, nil, 24*time.Hour), types.PredictionStateWaiting},
		{"recent pending on a growing exam waits", stalledPredictionRow(pending, nil, 23*time.Hour), types.PredictionStateWaiting},
		{"recent queued stays queued", stalledPredictionRow(queued, nil, 23*time.Hour), types.PredictionStateQueued},
		{"old but at study-service stays queued", stalledPredictionRow(queued, strPointer("ss-1"), 25*time.Hour), types.PredictionStateQueued},
		{"old running is running", stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusRunning, nil, 25*time.Hour), types.PredictionStateRunning},
		{"old completed is completed", stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusCompleted, nil, 25*time.Hour), types.PredictionStateCompleted},
		{"old cancelled is cancelled", stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusCancelled, nil, 25*time.Hour), types.PredictionStateCancelled},
	}
	for _, testCase := range cases {
		row := testCase.row
		require.Equal(t, testCase.expected, predictionExecutionState(&row, stalledBefore), testCase.name)
	}
}

func TestStalledModelsAreCountedAndDoNotMakeTheExamActive(t *testing.T) {
	stalled := stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusPending, nil, 72*time.Hour)
	completed := stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusCompleted, nil, 72*time.Hour)
	completed.ModelName, completed.ExecutionID = "DeepCORO-SYNTAX", "ex-c"
	repository := &predictionsRepositoryFake{
		pages:      []repositoryTypes.PredictionExamPage{{Total: 1, Exams: []repositoryTypes.PredictionExamRow{{StudyInstanceUID: "2.1", Total: 1}}}},
		executions: []repositoryTypes.PredictionExecutionRow{stalled, completed},
	}
	service := InferencePredictionsService{Now: pinnedNow, Repository: repository}
	page, err := service.ListPredictions(context.Background(), basePredictionsRequest(types.PredictionsViewAutomatic, "USER"))
	require.NoError(t, err)
	exam := page.Exams[0]
	require.False(t, exam.Active, "a stalled model will not progress")
	require.Equal(t, types.PredictionCounts{Total: 2, Stalled: 1, Completed: 1}, exam.Counts)
	require.Equal(t, types.PredictionStateStalled, exam.Models[0].State)
	require.True(t, exam.Models[0].Cancellable, "an administrator can close it from the page")

	// Next to a running model the exam is still active.
	running := stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusRunning, strPointer("ss-2"), time.Hour)
	running.ModelName = "DeepRV-CLIP"
	counts, active := countPredictionStates([]types.PredictionModel{
		{PredictionExecution: buildPredictionExecution(&stalled, "user-a", nil, predictionsTestNow.Add(-24*time.Hour))},
		{PredictionExecution: buildPredictionExecution(&running, "user-a", nil, predictionsTestNow.Add(-24*time.Hour))},
	})
	require.True(t, active)
	require.Equal(t, types.PredictionCounts{Total: 2, Stalled: 1, Running: 1}, counts)
}

func TestStalledAfterIsConfigurable(t *testing.T) {
	service := InferencePredictionsService{Now: pinnedNow, StalledAfter: 2 * time.Hour}
	require.Equal(t, predictionsTestNow.Add(-2*time.Hour), service.stalledBefore())
	service.StalledAfter = 0
	require.Equal(t, predictionsTestNow.Add(-24*time.Hour), service.stalledBefore(), "default 24 hours")

	for value, expected := range map[string]time.Duration{
		"": 24 * time.Hour, "48": 48 * time.Hour, " 6 ": 6 * time.Hour, "0.5": 30 * time.Minute,
		"0": 24 * time.Hour, "-3": 24 * time.Hour, "soon": 24 * time.Hour,
	} {
		t.Setenv(predictionsStalledAfterEnv, value)
		require.Equal(t, expected, ConfiguredPredictionsStalledAfter(), value)
	}
}

func TestExportPredictionsShowsStalledRuns(t *testing.T) {
	stalled := stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusQueued, nil, 72*time.Hour)
	repository := &predictionsRepositoryFake{
		pages:      []repositoryTypes.PredictionExamPage{{Total: 1, Exams: []repositoryTypes.PredictionExamRow{{StudyInstanceUID: "2.1", Total: 1}}}},
		executions: []repositoryTypes.PredictionExecutionRow{stalled},
	}
	var buffer bytes.Buffer
	request := basePredictionsRequest(types.PredictionsViewAutomatic, "USER")
	request.Status = "attention"
	_, err := (&InferencePredictionsService{Now: pinnedNow, Repository: repository}).ExportPredictions(context.Background(),
		request, func(bool) io.Writer { return &buffer })
	require.NoError(t, err)
	require.Equal(t, predictionsTestNow.Add(-24*time.Hour), repository.examQueries[0].StalledBefore)
	lines, err := csv.NewReader(&buffer).ReadAll()
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, "stalled", lines[1][10])
	require.Equal(t, "", lines[1][13])
}

func TestExpiredExecutionsAreShownCountedAndNotActive(t *testing.T) {
	expired := stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusSkipped, nil, 800*time.Hour)
	code := entity.InferenceIngestionProcessingJobSkipReasonExpiredNeverRan
	expired.SkipReasonCode, expired.SkipReasonMessage = &code, strPointer("Expired: did not complete within 30 days (automatic)")
	expired.RunPhase = entity.InferenceIngestionProcessingRunPhaseTerminal
	skipped := stalledPredictionRow(entity.InferenceIngestionProcessingJobStatusSkipped, nil, 800*time.Hour)
	other := entity.InferenceIngestionProcessingJobSkipReasonModelDisabled
	skipped.SkipReasonCode, skipped.ModelName, skipped.ExecutionID = &other, "DeepCORO-SYNTAX", "ex-k"

	stalledBefore := predictionsTestNow.Add(-24 * time.Hour)
	require.Equal(t, types.PredictionStateExpired, predictionExecutionState(&expired, stalledBefore))
	require.Equal(t, types.PredictionStateSkipped, predictionExecutionState(&skipped, stalledBefore))

	repository := &predictionsRepositoryFake{
		pages:      []repositoryTypes.PredictionExamPage{{Total: 1, Exams: []repositoryTypes.PredictionExamRow{{StudyInstanceUID: "2.1", Total: 1}}}},
		executions: []repositoryTypes.PredictionExecutionRow{expired, skipped},
	}
	page, err := (&InferencePredictionsService{Now: pinnedNow, Repository: repository}).ListPredictions(context.Background(),
		basePredictionsRequest(types.PredictionsViewAutomatic, "USER"))
	require.NoError(t, err)
	exam := page.Exams[0]
	require.False(t, exam.Active)
	require.Equal(t, types.PredictionCounts{Total: 2, Expired: 1, Skipped: 1}, exam.Counts)
	require.False(t, exam.Models[0].Cancellable)
	require.Equal(t, entity.InferenceIngestionProcessingJobSkipReasonExpiredNeverRan, exam.Models[0].SkipReason.Code)

	repository = &predictionsRepositoryFake{
		pages:      []repositoryTypes.PredictionExamPage{{Total: 1, Exams: []repositoryTypes.PredictionExamRow{{StudyInstanceUID: "2.1", Total: 1}}}},
		executions: []repositoryTypes.PredictionExecutionRow{expired},
	}
	var buffer bytes.Buffer
	_, err = (&InferencePredictionsService{Now: pinnedNow, Repository: repository}).ExportPredictions(context.Background(),
		basePredictionsRequest(types.PredictionsViewAutomatic, "USER"), func(bool) io.Writer { return &buffer })
	require.NoError(t, err)
	lines, err := csv.NewReader(&buffer).ReadAll()
	require.NoError(t, err)
	require.Equal(t, "expired", lines[1][10])
	require.Equal(t, "EXPIRED_NEVER_RAN: Expired: did not complete within 30 days (automatic)", lines[1][13])
}

func TestListPredictionsCountsOnlyTheModelsTheSessionMayList(t *testing.T) {
	service := InferencePredictionsService{Now: pinnedNow, Repository: predictionsFixture(), Users: &predictionsUsersFake{}}
	request := basePredictionsRequest(types.PredictionsViewMine, "USER")
	// Hide the waiting and queued models of the first exam (as for a research-only model).
	request.AllowModelName = func(name string) bool { return name != "CathEF-CLIP" && name != "DeepRV-CLIP" }

	page, err := service.ListPredictions(context.Background(), request)
	require.NoError(t, err)
	exam := page.Exams[0]
	for _, model := range exam.Models {
		require.NotContains(t, []string{"CathEF-CLIP", "DeepRV-CLIP"}, model.ModelName)
	}
	require.Equal(t, types.PredictionCounts{Total: len(exam.Models), Completed: len(exam.Models)}, exam.Counts)
	require.False(t, exam.Active, "a hidden running model does not make the exam active")
}
