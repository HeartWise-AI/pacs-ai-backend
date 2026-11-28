package service

import (
	"context"
	"errors"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apiError "api-pacs/internal/errors"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

const (
	defaultPredictionsPageSize = 25
	maximumPredictionsPageSize = 100
	maximumPredictionsOffset   = 100_000
	maximumPredictionsSearch   = 128
	maximumPredictionsModel    = 255
	maximumPredictionsHistory  = 50

	// DefaultPredictionsStalledAfter is how long after its run was created a
	// pending or queued execution without a study-service job is shown as
	// stalled ("never ran").
	DefaultPredictionsStalledAfter = 24 * time.Hour
	predictionsStalledAfterEnv     = "INFERENCE_PREDICTIONS_STALLED_AFTER_HOURS"
)

// ConfiguredPredictionsStalledAfter reads INFERENCE_PREDICTIONS_STALLED_AFTER_HOURS
// (a positive number of hours, default 24).
func ConfiguredPredictionsStalledAfter() time.Duration {
	value := strings.TrimSpace(os.Getenv(predictionsStalledAfterEnv))
	if value == "" {
		return DefaultPredictionsStalledAfter
	}
	hours, err := strconv.ParseFloat(value, 64)
	if err != nil || hours <= 0 || hours > 24*365 {
		log.Printf("[AI predictions] invalid %s=%q, using %s", predictionsStalledAfterEnv, value, DefaultPredictionsStalledAfter)
		return DefaultPredictionsStalledAfter
	}
	return time.Duration(hours * float64(time.Hour))
}

var predictionsModalityPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,16}$`)

// PredictionsUserDirectory resolves the names of the users who launched runs.
type PredictionsUserDirectory interface {
	GetTenantUsers(ctx context.Context, tenantID string) ([]userTypes.GetTenantUser, error)
}

// InferencePredictionsService serves the AI Predictions page: one row per
// exam with the latest execution of each model, earlier executions on
// demand, and a CSV export of the runs.
type InferencePredictionsService struct {
	Repository repository.InferencePredictionsRepositoryInterface
	// Users is optional; without it (or when it fails) names are left null.
	Users PredictionsUserDirectory
	// StalledAfter defaults to DefaultPredictionsStalledAfter when zero.
	StalledAfter time.Duration
	// Now defaults to time.Now; tests pin it.
	Now func() time.Time
}

// stalledBefore is the run creation time before which an execution that
// never reached study-service is shown as stalled.
func (service *InferencePredictionsService) stalledBefore() time.Time {
	now := time.Now
	if service.Now != nil {
		now = service.Now
	}
	after := service.StalledAfter
	if after <= 0 {
		after = DefaultPredictionsStalledAfter
	}
	return now().Add(-after)
}

// IsWorkspaceAdministrator reports whether a role may use the "all" view.
func IsWorkspaceAdministrator(role string) bool {
	role = strings.TrimSpace(role)
	return role == iamEntity.OwnerRole || role == iamEntity.AdminRole
}

// predictionsQuery is a validated, authorized list request.
type predictionsQuery struct {
	exams repositoryTypes.ListPredictionExams
	// lineScope keeps the export lines of the "Launched by" filter.
	lineScope  repositoryTypes.PredictionsScope
	lineUserID string
	view       types.PredictionsView
	userID     string
	canViewAll bool
	// stalledBefore is shared by the SQL status filter and the state mapping.
	stalledBefore time.Time
}

func parsePredictionsStatus(value string) (repositoryTypes.PredictionsExamStatus, bool) {
	switch strings.TrimSpace(value) {
	case "", "any":
		return repositoryTypes.PredictionsExamStatusAny, true
	case "active":
		return repositoryTypes.PredictionsExamStatusActive, true
	case "attention":
		return repositoryTypes.PredictionsExamStatusAttention, true
	case "completed":
		return repositoryTypes.PredictionsExamStatusCompleted, true
	default:
		return "", false
	}
}

// buildPredictionsQuery validates the request and enforces the view rules:
// "mine" always means the authenticated user, "all" needs ADMIN or OWNER, and
// "Launched by" is only accepted in "all".
func buildPredictionsQuery(data types.ListPredictions, maximumLimit int, stalledBefore time.Time) (predictionsQuery, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	userID := strings.TrimSpace(data.UserID)
	if tenantID == "" || userID == "" {
		return predictionsQuery{}, errors.New(apiError.UnauthorizedAccess)
	}
	query := predictionsQuery{userID: userID, canViewAll: data.CanViewAll || IsWorkspaceAdministrator(data.Role), stalledBefore: stalledBefore}

	view := types.PredictionsView(strings.TrimSpace(string(data.View)))
	if view == "" {
		view = types.PredictionsViewMine
	}
	launchedBy := strings.TrimSpace(data.LaunchedBy)
	if launchedBy == "any" {
		launchedBy = ""
	}
	scope := repositoryTypes.PredictionsScopeAll
	requestedBy := ""
	switch view {
	case types.PredictionsViewMine:
		scope, requestedBy = repositoryTypes.PredictionsScopeRequestedBy, userID
	case types.PredictionsViewAutomatic:
		scope = repositoryTypes.PredictionsScopeAutomatic
	case types.PredictionsViewAll:
		if !query.canViewAll {
			return predictionsQuery{}, errors.New(apiError.ForbiddenAccess)
		}
	default:
		return predictionsQuery{}, errors.New(apiError.InvalidPayload)
	}
	if launchedBy != "" {
		if view != types.PredictionsViewAll {
			return predictionsQuery{}, errors.New(apiError.InvalidPayload)
		}
		switch {
		case launchedBy == "automatic":
			scope = repositoryTypes.PredictionsScopeAutomatic
		case launchedBy == "people":
			scope = repositoryTypes.PredictionsScopePeople
		case strings.HasPrefix(launchedBy, "user:") && strings.TrimSpace(strings.TrimPrefix(launchedBy, "user:")) != "" &&
			len(launchedBy) <= 140:
			scope, requestedBy = repositoryTypes.PredictionsScopeRequestedBy, strings.TrimSpace(strings.TrimPrefix(launchedBy, "user:"))
		default:
			return predictionsQuery{}, errors.New(apiError.InvalidPayload)
		}
		query.lineScope, query.lineUserID = scope, requestedBy
	}

	status, ok := parsePredictionsStatus(data.Status)
	if !ok {
		return predictionsQuery{}, errors.New(apiError.InvalidPayload)
	}
	search := strings.TrimSpace(data.Search)
	model := strings.TrimSpace(data.Model)
	modality := strings.TrimSpace(data.Modality)
	if utf8.RuneCountInString(search) > maximumPredictionsSearch || len(model) > maximumPredictionsModel ||
		(modality != "" && !predictionsModalityPattern.MatchString(modality)) {
		return predictionsQuery{}, errors.New(apiError.InvalidPayload)
	}
	if data.From != nil && data.To != nil && !data.To.After(*data.From) {
		return predictionsQuery{}, errors.New(apiError.InvalidPayload)
	}
	limit := data.Limit
	if limit == 0 {
		limit = defaultPredictionsPageSize
	}
	if limit < 0 || data.Offset < 0 || data.Offset > maximumPredictionsOffset {
		return predictionsQuery{}, errors.New(apiError.InvalidPayload)
	}
	if limit > maximumLimit {
		return predictionsQuery{}, errors.New(apiError.MaximumLimitReached)
	}

	query.view = view
	query.exams = repositoryTypes.ListPredictionExams{
		TenantID: tenantID, Scope: scope, RequestedByUserID: requestedBy,
		ModelName: model, Status: status, Modality: modality, Search: search,
		From: data.From, To: data.To, StalledBefore: stalledBefore, Limit: limit, Offset: data.Offset,
	}
	return query, nil
}

// ListPredictions returns one page of exams for the requested view.
func (service *InferencePredictionsService) ListPredictions(ctx context.Context, data types.ListPredictions) (types.PredictionsPage, error) {
	query, err := buildPredictionsQuery(data, maximumPredictionsPageSize, service.stalledBefore())
	if err != nil {
		return types.PredictionsPage{}, err
	}
	page, err := service.Repository.ListPredictionExams(ctx, query.exams)
	if err != nil {
		return types.PredictionsPage{}, err
	}

	uids := make([]string, 0, len(page.Exams))
	for _, exam := range page.Exams {
		uids = append(uids, exam.StudyInstanceUID)
	}
	executions, err := service.Repository.ListPredictionExecutions(ctx, repositoryTypes.ListPredictionExecutions{
		TenantID: query.exams.TenantID, StudyInstanceUIDs: uids, ModelName: query.exams.ModelName, LatestOnly: true,
	})
	if err != nil {
		return types.PredictionsPage{}, err
	}
	launchers, err := service.Repository.ListPredictionLaunchers(ctx, query.exams.TenantID, uids)
	if err != nil {
		return types.PredictionsPage{}, err
	}
	names := service.userNames(ctx, query.exams.TenantID, executions, launchers)

	modelsByExam := map[string][]types.PredictionModel{}
	for index := range executions {
		row := &executions[index]
		modelsByExam[row.StudyInstanceUID] = append(modelsByExam[row.StudyInstanceUID], types.PredictionModel{
			PredictionExecution: buildPredictionExecution(row, query.userID, names, query.stalledBefore),
			EarlierRuns:         row.EarlierRuns,
		})
	}
	launchersByExam := map[string][]types.PredictionLauncher{}
	for _, row := range launchers {
		launchersByExam[row.StudyInstanceUID] = appendPredictionLauncher(launchersByExam[row.StudyInstanceUID], row, query.userID, names)
	}

	result := types.PredictionsPage{
		View: query.view, Exams: make([]types.PredictionExam, 0, len(page.Exams)),
		Limit: query.exams.Limit, Offset: query.exams.Offset, Total: page.Total,
		HasMore: query.exams.Offset+len(page.Exams) < page.Total, CanViewAll: query.canViewAll,
	}
	for _, row := range page.Exams {
		models := modelsByExam[row.StudyInstanceUID]
		if models == nil {
			models = []types.PredictionModel{}
		}
		if data.AllowModelName != nil {
			// Research-only models withheld from the session are left out before counting.
			kept := make([]types.PredictionModel, 0, len(models))
			for _, model := range models {
				if data.AllowModelName(model.ModelName) {
					kept = append(kept, model)
				}
			}
			models = kept
		}
		examLaunchers := launchersByExam[row.StudyInstanceUID]
		if examLaunchers == nil {
			examLaunchers = []types.PredictionLauncher{}
		}
		exam := types.PredictionExam{
			StudyInstanceUID: row.StudyInstanceUID, PatientID: row.PatientID, AccessionNumber: row.AccessionNumber,
			ModalitiesInStudy: row.ModalitiesInStudy, StudyDate: row.StudyDate, LastActivityAt: row.LastActivityAt,
			Models: models, Launchers: examLaunchers,
		}
		exam.Counts, exam.Active = countPredictionStates(models)
		result.Exams = append(result.Exams, exam)
	}
	return result, nil
}

// GetPredictionModelHistory returns the executions of one model on one exam,
// newest first. Like the run history of a study it is open to every user of
// the tenant.
func (service *InferencePredictionsService) GetPredictionModelHistory(ctx context.Context, data types.GetPredictionModelHistory) (types.PredictionHistoryPage, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	userID := strings.TrimSpace(data.UserID)
	if tenantID == "" || userID == "" {
		return types.PredictionHistoryPage{}, errors.New(apiError.UnauthorizedAccess)
	}
	studyInstanceUID := strings.TrimSpace(data.StudyInstanceUID)
	model := strings.TrimSpace(data.Model)
	limit := data.Limit
	if limit == 0 {
		limit = maximumPredictionsHistory
	}
	if studyInstanceUID == "" || len(studyInstanceUID) > maximumViewerStudyUIDLength || model == "" ||
		len(model) > maximumPredictionsModel || limit < 0 || data.Offset < 0 || data.Offset > maximumPredictionsOffset {
		return types.PredictionHistoryPage{}, errors.New(apiError.InvalidPayload)
	}
	if limit > maximumPredictionsHistory {
		return types.PredictionHistoryPage{}, errors.New(apiError.MaximumLimitReached)
	}

	rows, err := service.Repository.ListPredictionModelHistory(ctx, repositoryTypes.ListPredictionModelHistory{
		TenantID: tenantID, StudyInstanceUID: studyInstanceUID, ModelName: model, Limit: limit, Offset: data.Offset,
	})
	if err != nil {
		return types.PredictionHistoryPage{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	names := service.userNames(ctx, tenantID, rows, nil)
	stalledBefore := service.stalledBefore()
	page := types.PredictionHistoryPage{
		StudyInstanceUID: studyInstanceUID, ModelName: model,
		Executions: make([]types.PredictionExecution, 0, len(rows)),
		Limit:      limit, Offset: data.Offset, HasMore: hasMore,
	}
	for index := range rows {
		page.Executions = append(page.Executions, buildPredictionExecution(&rows[index], userID, names, stalledBefore))
	}
	return page, nil
}

// userNames maps the requesting users of the rows to their names. A failed
// lookup is logged and leaves every name unknown.
func (service *InferencePredictionsService) userNames(
	ctx context.Context,
	tenantID string,
	executions []repositoryTypes.PredictionExecutionRow,
	launchers []repositoryTypes.PredictionLauncherRow,
) map[string]string {
	names := map[string]string{}
	needed := false
	for index := range executions {
		needed = needed || trimmedPointerValue(executions[index].RequestedByUserID) != ""
	}
	for _, launcher := range launchers {
		needed = needed || trimmedPointerValue(launcher.RequestedByUserID) != ""
	}
	if !needed || service.Users == nil {
		return names
	}
	users, err := service.Users.GetTenantUsers(ctx, tenantID)
	if err != nil {
		log.Printf("[AI predictions] user name lookup failed err=%v", err)
		return names
	}
	for _, user := range users {
		if user.TenantID != "" && user.TenantID != tenantID {
			continue
		}
		if name := strings.TrimSpace(user.Name); name != "" {
			names[strings.TrimSpace(user.ID)] = name
		}
	}
	return names
}

// predictionStalled reports whether an execution never ran: still pending
// or queued without a study-service job although its run was created before
// stalledBefore. The re-dispatch recovery skips executions that old, so they
// will not progress. The exam list status filter applies the same rule in SQL.
func predictionStalled(row *repositoryTypes.PredictionExecutionRow, stalledBefore time.Time) bool {
	return (row.Status == entity.InferenceIngestionProcessingJobStatusPending ||
		row.Status == entity.InferenceIngestionProcessingJobStatusQueued) &&
		trimmedPointerValue(row.StudyServiceJobID) == "" && row.RunCreatedAt.Before(stalledBefore)
}

// predictionExecutionState maps a stored execution to the state the page
// shows. A pending execution never dispatched while its exam is still
// receiving images waits for the exam to finish; one that never reached
// study-service long after its run was created is stalled, and one the expiry
// of automatic runs closed is expired.
func predictionExecutionState(row *repositoryTypes.PredictionExecutionRow, stalledBefore time.Time) types.PredictionState {
	if predictionStalled(row, stalledBefore) {
		return types.PredictionStateStalled
	}
	switch row.Status {
	case entity.InferenceIngestionProcessingJobStatusPending:
		if trimmedPointerValue(row.StudyServiceJobID) == "" && row.CandidateStatus != nil &&
			(*row.CandidateStatus == entity.InferenceIngestionCandidateStatusGrowing ||
				*row.CandidateStatus == entity.InferenceIngestionCandidateStatusDiscovered) {
			return types.PredictionStateWaiting
		}
		return types.PredictionStateQueued
	case entity.InferenceIngestionProcessingJobStatusQueued:
		return types.PredictionStateQueued
	case entity.InferenceIngestionProcessingJobStatusRunning:
		return types.PredictionStateRunning
	case entity.InferenceIngestionProcessingJobStatusCompleted:
		return types.PredictionStateCompleted
	case entity.InferenceIngestionProcessingJobStatusFailed:
		return types.PredictionStateFailed
	case entity.InferenceIngestionProcessingJobStatusSkipped:
		if row.SkipReasonCode != nil && *row.SkipReasonCode == entity.InferenceIngestionProcessingJobSkipReasonExpiredNeverRan {
			return types.PredictionStateExpired
		}
		return types.PredictionStateSkipped
	case entity.InferenceIngestionProcessingJobStatusCancelled:
		return types.PredictionStateCancelled
	default:
		return types.PredictionStateQueued
	}
}

func buildPredictionExecution(
	row *repositoryTypes.PredictionExecutionRow,
	userID string,
	names map[string]string,
	stalledBefore time.Time,
) types.PredictionExecution {
	execution := entity.InferenceIngestionProcessingJob{
		Status: row.Status, StudyServiceJobID: row.StudyServiceJobID,
		SkipReasonCode: row.SkipReasonCode, SkipReasonMessage: row.SkipReasonMessage,
	}
	studyServiceJobMissing := entity.ProcessingRunExecutionStudyServiceJobMissing(row.RunAttentionReasons, execution)
	result := types.PredictionExecution{
		RunID: row.RunID, ExecutionID: row.ExecutionID, RunNumber: row.RunNumber, Trigger: row.RunTrigger,
		ModelName: row.ModelName, ModelVersion: row.ModelVersion, Modality: row.Modality,
		State: predictionExecutionState(row, stalledBefore), Status: row.Status, ErrorMessage: row.ErrorMessage,
		SkipReason: execution.GetSkipReason(),
		Cancellable: row.RunPhase != entity.InferenceIngestionProcessingRunPhaseTerminal &&
			execution.IsCancellableBeforeCompute(studyServiceJobMissing),
		CancelledAt: row.CancelledAt, RunCreatedAt: row.RunCreatedAt, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
	}
	if requester := trimmedPointerValue(row.RequestedByUserID); requester != "" {
		result.RequestedByUserID = &requester
		result.RequestedByYou = requester == userID
		if name, ok := names[requester]; ok {
			result.RequestedByName = &name
		}
	}
	if row.StartedAt != nil && row.CompletedAt != nil && !row.CompletedAt.Before(*row.StartedAt) {
		seconds := row.CompletedAt.Sub(*row.StartedAt).Round(time.Millisecond).Seconds()
		result.DurationSeconds = &seconds
	}
	return result
}

// appendPredictionLauncher merges the automatic triggers into one launcher
// and keeps one launcher per user and trigger, in order of first run.
func appendPredictionLauncher(
	launchers []types.PredictionLauncher,
	row repositoryTypes.PredictionLauncherRow,
	userID string,
	names map[string]string,
) []types.PredictionLauncher {
	requester := trimmedPointerValue(row.RequestedByUserID)
	if types.IsAutomaticWorklistTrigger(row.RunTrigger) || requester == "" {
		for index := range launchers {
			if launchers[index].Kind == "automatic" {
				launchers[index].Runs += row.Runs
				if row.FirstAt.Before(launchers[index].FirstAt) {
					launchers[index].FirstAt = row.FirstAt
				}
				return launchers
			}
		}
		return insertPredictionLauncher(launchers, types.PredictionLauncher{
			Kind: "automatic", Trigger: entity.InferenceIngestionProcessingRunTriggerAuto, Runs: row.Runs, FirstAt: row.FirstAt,
		})
	}
	launcher := types.PredictionLauncher{
		Kind: "user", Trigger: row.RunTrigger, UserID: &requester, You: requester == userID,
		Runs: row.Runs, FirstAt: row.FirstAt,
	}
	if name, ok := names[requester]; ok {
		launcher.Name = &name
	}
	return insertPredictionLauncher(launchers, launcher)
}

func insertPredictionLauncher(launchers []types.PredictionLauncher, launcher types.PredictionLauncher) []types.PredictionLauncher {
	position := len(launchers)
	for index := range launchers {
		if launcher.FirstAt.Before(launchers[index].FirstAt) {
			position = index
			break
		}
	}
	launchers = append(launchers, types.PredictionLauncher{})
	copy(launchers[position+1:], launchers[position:])
	launchers[position] = launcher
	return launchers
}

// countPredictionStates counts the latest state of each model; a stalled
// model does not make the exam active.
func countPredictionStates(models []types.PredictionModel) (types.PredictionCounts, bool) {
	counts := types.PredictionCounts{Total: len(models)}
	for _, model := range models {
		switch model.State {
		case types.PredictionStateWaiting:
			counts.Waiting++
		case types.PredictionStateQueued:
			counts.Queued++
		case types.PredictionStateStalled:
			counts.Stalled++
		case types.PredictionStateRunning:
			counts.Running++
		case types.PredictionStateCompleted:
			counts.Completed++
		case types.PredictionStateFailed:
			counts.Failed++
		case types.PredictionStateSkipped:
			counts.Skipped++
		case types.PredictionStateCancelled:
			counts.Cancelled++
		case types.PredictionStateExpired:
			counts.Expired++
		}
	}
	return counts, counts.Waiting+counts.Queued+counts.Running > 0
}
