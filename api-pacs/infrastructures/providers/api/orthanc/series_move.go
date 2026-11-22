package orthanc

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-pacs/infrastructures/providers/api/orthanc/types"
)

// Retrieving a study from the hospital PACS.
//
// One C-MOVE job per series, in series-number order, so the series the viewer
// shows first lands first. Orthanc runs the jobs a few at a time
// (ORTHANC__CONCURRENT_JOBS) and picks the highest priority first: a study a
// person is waiting for comes before automatic ingestion, and within a study
// the first series comes first.
//
// The jobs are not permissive: a refused or aborted association fails the job
// instead of ending as Success with images missing. A failed job, or one that
// ended with fewer images than the PACS announced, is moved again (a new
// Orthanc job) with backoff, and is only reported as failed once the attempts
// are used up. Callers keep the first job's ID: its status is read from the
// latest attempt. The moves are saved in Postgres and a background loop reads
// them (move_supervisor.go), so retries go on when nobody polls a job and
// after api-pacs restarts.
//
// Orthanc never preempts a running job, so background moves (ingestion, bulk
// reprocess) are kept to fewer than Orthanc's job slots: the others wait in
// api-pacs and a slot stays free for a study someone is waiting for.

const (
	// Priority of a retrieve someone is waiting for (the viewer / work list).
	interactiveMovePriority = 1000
	// Priority of an automatic retrieve (ingestion).
	backgroundMovePriority = 0
	// Attempts per series move, the first one included.
	maxSeriesMoveAttempts = 4
)

// Wait before attempt n+1 (index n-1).
var seriesMoveBackoff = []time.Duration{3 * time.Second, 10 * time.Second, 30 * time.Second}

// orderSeriesForMove sorts the series by series number, the order the viewer
// shows them in; series without a number go last, in the PACS's order.
func orderSeriesForMove(series []types.QueryModalitySeriesAnswersResponse) []types.QueryModalitySeriesAnswersResponse {
	ordered := make([]types.QueryModalitySeriesAnswersResponse, len(series))
	copy(ordered, series)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, aok := parseDICOMInt(ordered[i].SeriesNumber)
		b, bok := parseDICOMInt(ordered[j].SeriesNumber)
		if aok != bok {
			return aok
		}
		return aok && a < b
	})
	return ordered
}

func parseDICOMInt(value string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return n, true
}

// seriesMovePriority gives the first series the highest priority.
func seriesMovePriority(interactive bool, index, total int) int {
	base := backgroundMovePriority
	if interactive {
		base = interactiveMovePriority
	}
	return base + (total - index)
}

// seriesMoveRequest is the body of POST /modalities/{id}/move for one series.
func seriesMoveRequest(series types.QueryModalitySeriesAnswersResponse, targetAet string, priority int) map[string]interface{} {
	return map[string]interface{}{
		"Level": "Series",
		"Resources": []map[string]string{
			{
				"StudyInstanceUID":  series.StudyInstanceUID,
				"SeriesInstanceUID": series.SeriesInstanceUID,
			},
		},
		"Asynchronous": true,
		"Permissive":   false,
		"Priority":     priority,
		"Synchronous":  false,
		"TargetAet":    targetAet,
		"Timeout":      0,
	}
}

// movePart is one series of a move job and the images the PACS announced.
type movePart struct {
	studyInstanceUID  string
	seriesInstanceUID string
	expected          int // 0 when the PACS did not say
}

// trackedMove is a move job this process started. It is saved in Postgres
// (moveStore) on every change, so a restart of api-pacs keeps retrying it.
type trackedMove struct {
	modalityID        string
	request           map[string]interface{} // the move, to send again
	currentJobID      string                 // Orthanc job of the latest attempt; "" before the first one
	parts             []movePart             // the series this job moves
	expectedInstances int                    // images the PACS announced for them; 0 when unknown
	attempts          int                    // moves sent to Orthanc
	retryAt           time.Time              // zero unless a resubmission is waiting
	resubmitting      time.Time              // when a reader started sending the next attempt; zero if none
	verified          bool                   // success checked against the local copy
	trackedAt         time.Time

	// Background (ingestion, bulk reprocess) moves wait in api-pacs for a
	// free background slot (see move_supervisor.go) instead of in Orthanc.
	background bool
	queued     bool // waiting for a background slot
	queuedAt   time.Time
	priority   int

	// Set once the move has its final state; reported without asking Orthanc.
	done           bool
	finalState     string
	finalError     string
	completionTime string
}

// How long a move is remembered: callers stop polling long before.
const trackedMoveLifetime = 24 * time.Hour

type moveTracker struct {
	mu    sync.Mutex
	moves map[string]*trackedMove
	now   func() time.Time
	store moveStore // nil: kept in memory only
}

func newMoveTracker() *moveTracker {
	return &moveTracker{moves: map[string]*trackedMove{}, now: time.Now}
}

var seriesMoves = newMoveTracker()

func newTrackedMove(now time.Time, modalityID string, request map[string]interface{}, series []types.QueryModalitySeriesAnswersResponse) *trackedMove {
	parts := make([]movePart, 0, len(series))
	expected := 0
	for _, item := range series {
		count, _ := parseDICOMInt(item.NumberOfSeriesRelatedInstances)
		parts = append(parts, movePart{studyInstanceUID: item.StudyInstanceUID, seriesInstanceUID: item.SeriesInstanceUID, expected: count})
		expected += count
	}
	return &trackedMove{
		trackedAt:         now,
		modalityID:        modalityID,
		request:           request,
		parts:             parts,
		expectedInstances: expected,
		priority:          requestPriority(request),
	}
}

// track records a move already sent to Orthanc as job jobID.
func (t *moveTracker) track(jobID, modalityID string, request map[string]interface{}, series ...types.QueryModalitySeriesAnswersResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.forgetOldLocked(now)
	move := newTrackedMove(now, modalityID, request, series)
	move.currentJobID = jobID
	move.attempts = 1
	t.moves[jobID] = move
	t.saveLocked(jobID, move)
}

// enqueue records a background move that waits for a free background slot
// before it is sent to Orthanc. jobID is the ID its caller keeps.
func (t *moveTracker) enqueue(jobID, modalityID string, request map[string]interface{}, series ...types.QueryModalitySeriesAnswersResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.forgetOldLocked(now)
	move := newTrackedMove(now, modalityID, request, series)
	move.background = true
	move.queued = true
	move.queuedAt = now
	t.moves[jobID] = move
	t.saveLocked(jobID, move)
}

func (t *moveTracker) forgetOldLocked(now time.Time) {
	for id, move := range t.moves {
		if now.Sub(move.trackedAt) > trackedMoveLifetime {
			delete(t.moves, id)
		}
	}
}

// moveAction is what the status reader must do for a job.
type moveAction int

const (
	// Report Orthanc's state as it is.
	moveReport moveAction = iota
	// Report the job as pending: a resubmission is waiting for its backoff.
	moveWaitRetry
	// Move the series again now and report it as pending.
	moveResubmit
	// Check the local copy has every image before reporting success.
	moveVerify
)

// current is the Orthanc job holding the latest attempt of a move.
func (t *moveTracker) current(jobID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if move, ok := t.moves[jobID]; ok && move.currentJobID != "" {
		return move.currentJobID
	}
	return jobID
}

// settledView answers for a move without asking Orthanc: one that has its
// final state, or one waiting in api-pacs for a background slot.
func (t *moveTracker) settledView(jobID string) (types.GetJobResponse, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok {
		return types.GetJobResponse{}, false
	}
	view := types.GetJobResponse{ID: jobID, Type: dicomMoveJobType, Priority: uint(max(move.priority, 0))}
	switch {
	case move.done:
		view.State = move.finalState
		view.ErrorDescription = move.finalError
		view.CompletionTime = move.completionTime
		if move.finalState == string(types.JobSuccess) {
			view.Progress = 100
		}
		return view, true
	case move.queued:
		view.State = string(types.JobPending)
		return view, true
	}
	return types.GetJobResponse{}, false
}

// decide looks at the state of a move's latest attempt (job is that attempt;
// jobID the ID the caller holds) and says what to do with it.
func (t *moveTracker) decide(jobID string, job types.GetJobResponse) moveAction {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok || move.done {
		return moveReport
	}
	if !move.retryAt.IsZero() {
		// An attempt failed earlier and the next one waits for its backoff.
		return t.retryLocked(jobID, move)
	}
	switch job.State {
	case string(types.JobFailure):
		return t.retryLocked(jobID, move)
	case string(types.JobSuccess):
		if move.verified || move.expectedInstances == 0 {
			return moveReport
		}
		return moveVerify
	default:
		return moveReport
	}
}

// failed records that the latest attempt ended with images missing.
func (t *moveTracker) failed(jobID string) moveAction {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok {
		return moveReport
	}
	return t.retryLocked(jobID, move)
}

func (t *moveTracker) retryLocked(jobID string, move *trackedMove) moveAction {
	if !move.resubmitting.IsZero() && t.now().Sub(move.resubmitting) < resubmitClaimLifetime {
		// Another reader is sending the next attempt right now.
		return moveWaitRetry
	}
	if move.attempts >= maxSeriesMoveAttempts {
		// The caller reports the failure; finish records it.
		return moveReport
	}
	if move.retryAt.IsZero() {
		move.retryAt = t.now().Add(backoffAfter(move.attempts))
		t.saveLocked(jobID, move)
	}
	if t.now().Before(move.retryAt) {
		return moveWaitRetry
	}
	// This reader sends the next attempt; others wait until it is recorded,
	// so the viewer and ingestion polling one job never both move it again.
	move.resubmitting = t.now()
	return moveResubmit
}

// A reader that claimed a resubmission and never reported back (it crashed)
// stops blocking the move after this long.
const resubmitClaimLifetime = time.Minute

// backoffAfter is the wait before the attempt that follows attempt n.
func backoffAfter(attempts int) time.Duration {
	return seriesMoveBackoff[min(max(attempts-1, 0), len(seriesMoveBackoff)-1)]
}

// retryRequest is what to send for the next attempt of a move.
type retryRequest struct {
	modalityID string
	request    map[string]interface{}
	background bool
}

// retry returns what to send for the next attempt.
func (t *moveTracker) retry(jobID string) (retryRequest, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, found := t.moves[jobID]
	if !found {
		return retryRequest{}, false
	}
	return retryRequest{modalityID: move.modalityID, request: move.request, background: move.background}, true
}

// resubmitted records the Orthanc job of a new attempt.
func (t *moveTracker) resubmitted(jobID, newJobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if move, ok := t.moves[jobID]; ok {
		move.attempts++
		move.retryAt = time.Time{}
		move.resubmitting = time.Time{}
		move.currentJobID = newJobID
		t.saveLocked(jobID, move)
	}
}

// requeue puts a background move's next attempt back in the background queue:
// it is sent when a background slot is free.
func (t *moveTracker) requeue(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if move, ok := t.moves[jobID]; ok {
		move.queued = true
		move.queuedAt = t.now()
		move.retryAt = time.Time{}
		move.resubmitting = time.Time{}
		t.saveLocked(jobID, move)
	}
}

// resubmitFailed records that sending the next attempt failed (the PACS or
// Orthanc did not take it). It counts as an attempt and waits for the next
// backoff, so a PACS that is down is not asked again on every poll and the
// move is reported failed once the attempts are used up.
func (t *moveTracker) resubmitFailed(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if move, ok := t.moves[jobID]; ok {
		move.attempts++
		move.resubmitting = time.Time{}
		move.retryAt = t.now().Add(backoffAfter(move.attempts))
		t.saveLocked(jobID, move)
	}
}

// verifiedCount records the local image count after a successful move. It
// returns false when images are missing; the move then counts as failed.
func (t *moveTracker) verifiedCount(jobID string, localInstances int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok {
		return true
	}
	if localInstances >= move.expectedInstances {
		move.verified = true
		t.saveLocked(jobID, move)
		return true
	}
	return false
}

// finish records the state reported for a move once it is final: a failure,
// or a success checked against the local copy (or with no count to check).
func (t *moveTracker) finish(jobID string, view types.GetJobResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok || move.done {
		return
	}
	switch view.State {
	case string(types.JobFailure):
	case string(types.JobSuccess):
		if !move.verified && move.expectedInstances > 0 {
			return
		}
	default:
		return
	}
	t.finishLocked(jobID, move, view.State, view.ErrorDescription, view.CompletionTime)
}

func (t *moveTracker) finishLocked(jobID string, move *trackedMove, state, errorDescription, completionTime string) {
	move.done = true
	move.queued = false
	move.finalState = state
	move.finalError = errorDescription
	move.completionTime = completionTime
	if move.completionTime == "" {
		move.completionTime = t.now().UTC().Format("20060102T150405")
	}
	t.saveLocked(jobID, move)
}

// parts returns the series of a move with the images announced for each.
func (t *moveTracker) parts(jobID string) ([]movePart, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, found := t.moves[jobID]
	if !found {
		return nil, 0, false
	}
	return append([]movePart(nil), move.parts...), move.expectedInstances, true
}

// --- the background queue ----------------------------------------------------

// queuedMove is a background move waiting for a slot.
type queuedMove struct {
	jobID      string
	modalityID string
	request    map[string]interface{}
	parts      []movePart
}

// waiting lists the background moves that may be sent now, oldest first and,
// within one retrieve, the first series first.
func (t *moveTracker) waiting() []queuedMove {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	type entry struct {
		queuedMove
		queuedAt time.Time
		priority int
	}
	var entries []entry
	for id, move := range t.moves {
		if !move.queued || move.done || now.Before(move.retryAt) {
			continue
		}
		entries = append(entries, entry{
			queuedMove: queuedMove{jobID: id, modalityID: move.modalityID, request: move.request, parts: append([]movePart(nil), move.parts...)},
			queuedAt:   move.queuedAt,
			priority:   move.priority,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].queuedAt.Equal(entries[j].queuedAt) {
			return entries[i].queuedAt.Before(entries[j].queuedAt)
		}
		if entries[i].priority != entries[j].priority {
			return entries[i].priority > entries[j].priority
		}
		return entries[i].jobID < entries[j].jobID
	})
	out := make([]queuedMove, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.queuedMove)
	}
	return out
}

// dispatched records the Orthanc job a queued move was sent as.
func (t *moveTracker) dispatched(jobID, orthancJobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if move, ok := t.moves[jobID]; ok {
		move.queued = false
		move.attempts++
		move.currentJobID = orthancJobID
		move.retryAt = time.Time{}
		move.resubmitting = time.Time{}
		t.saveLocked(jobID, move)
	}
}

// dispatchFailed records that Orthanc did not take a queued move. It counts
// as an attempt; the move waits for the next backoff, or fails once the
// attempts are used up.
func (t *moveTracker) dispatchFailed(jobID, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok {
		return
	}
	move.attempts++
	if move.attempts >= maxSeriesMoveAttempts {
		t.finishLocked(jobID, move, string(types.JobFailure), "Cannot send the move to the PACS: "+reason, "")
		return
	}
	move.retryAt = t.now().Add(backoffAfter(move.attempts))
	t.saveLocked(jobID, move)
}

// completeWithoutMove records that a queued move's images were all stored
// (another retrieve brought them) before it was sent.
func (t *moveTracker) completeWithoutMove(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if move, ok := t.moves[jobID]; ok {
		move.verified = true
		t.finishLocked(jobID, move, string(types.JobSuccess), "", "")
	}
}

// active lists the moves sent to Orthanc that have no final state yet.
func (t *moveTracker) active() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ids []string
	for id, move := range t.moves {
		if !move.done && !move.queued && move.currentJobID != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// requestPriority reads the Orthanc priority of a move request, as built here
// (int) or read back from JSON (float64).
func requestPriority(request map[string]interface{}) int {
	switch value := request["Priority"].(type) {
	case int:
		return value
	case float64:
		return int(value)
	}
	return 0
}

const dicomMoveJobType = "DicomMoveScu"

// pendingView is how a job waiting for a retry is reported to callers.
func pendingView(job types.GetJobResponse) types.GetJobResponse {
	job.State = string(types.JobPending)
	job.Progress = 0
	job.ErrorCode = 0
	job.ErrorDescription = ""
	job.ErrorDetails = nil
	job.CompletionTime = ""
	return job
}

// incompleteView reports a move whose images are still missing locally after
// its last attempt.
func incompleteView(job types.GetJobResponse, local, expected int) types.GetJobResponse {
	job.State = string(types.JobFailure)
	job.ErrorDescription = "Series incomplete after retrieve: " + strconv.Itoa(local) + " of " + strconv.Itoa(expected) + " images"
	return job
}

// --- moving only what is missing ---------------------------------------------

// localSeries is what the local store holds of one series.
type localSeries struct {
	orthancID string
	instances int
}

type moveKind int

const (
	// The series is not stored: move it whole.
	moveWholeSeries moveKind = iota
	// Part of the series is stored: move only the missing images.
	moveMissingInstances
)

// plannedMove is one series to move, in retrieve order.
type plannedMove struct {
	series types.QueryModalitySeriesAnswersResponse
	kind   moveKind
	local  localSeries
}

// planStudyMoves compares the PACS's series (in retrieve order) with the local
// store and keeps only what must move: a series that is not stored is moved
// whole; one with fewer local images than the PACS announces has its missing
// images moved; a complete series is skipped. When the PACS does not give an
// image count, a series that is stored at all counts as complete.
func planStudyMoves(remote []types.QueryModalitySeriesAnswersResponse, local map[string]localSeries) []plannedMove {
	var plan []plannedMove
	for _, series := range remote {
		stored, ok := local[series.SeriesInstanceUID]
		if !ok || stored.instances == 0 {
			plan = append(plan, plannedMove{series: series, kind: moveWholeSeries})
			continue
		}
		expected, known := parseDICOMInt(series.NumberOfSeriesRelatedInstances)
		if known && stored.instances < expected {
			plan = append(plan, plannedMove{series: series, kind: moveMissingInstances, local: stored})
		}
	}
	return plan
}

// missingInstances lists the PACS's images of a series that are not stored.
func missingInstances(remote, local []string) []string {
	have := make(map[string]bool, len(local))
	for _, uid := range local {
		have[uid] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, uid := range remote {
		if uid == "" || have[uid] || seen[uid] {
			continue
		}
		seen[uid] = true
		missing = append(missing, uid)
	}
	return missing
}

// seriesInstances is the images to move of one partly stored series.
type seriesInstances struct {
	series          types.QueryModalitySeriesAnswersResponse
	sopInstanceUIDs []string
}

// instancesBatchMoveRequest moves the given images of several series of one
// study in one job (one association to the PACS).
func instancesBatchMoveRequest(items []seriesInstances, targetAet string, priority int) map[string]interface{} {
	request := instancesMoveRequest(items[0].series, items[0].sopInstanceUIDs, targetAet, priority)
	resources := request["Resources"].([]map[string]string)
	for _, item := range items[1:] {
		for _, sop := range item.sopInstanceUIDs {
			resources = append(resources, map[string]string{
				"StudyInstanceUID":  item.series.StudyInstanceUID,
				"SeriesInstanceUID": item.series.SeriesInstanceUID,
				"SOPInstanceUID":    sop,
			})
		}
	}
	request["Resources"] = resources
	return request
}

// instancesMoveRequest moves the given images of one series in one job (one
// association to the PACS).
func instancesMoveRequest(series types.QueryModalitySeriesAnswersResponse, sopInstanceUIDs []string, targetAet string, priority int) map[string]interface{} {
	resources := make([]map[string]string, 0, len(sopInstanceUIDs))
	for _, sop := range sopInstanceUIDs {
		resources = append(resources, map[string]string{
			"StudyInstanceUID":  series.StudyInstanceUID,
			"SeriesInstanceUID": series.SeriesInstanceUID,
			"SOPInstanceUID":    sop,
		})
	}
	request := seriesMoveRequest(series, targetAet, priority)
	request["Level"] = "Instance"
	request["Resources"] = resources
	return request
}

// Jobs that share the series after the first one. Measured on MHI against the
// hospital PACS (20-image angio, 19 series): the first series alone plus the
// rest in 3 jobs had every image in 12.1 s, against 18.0 s for one study-level
// move, 21-42 s for one job per series and 132 s for one job per image (an
// echo study), with no image missed and no refused association in any setting.
const restSeriesJobs = 3

// batchMoves groups the planned moves into at most 1+restSeriesJobs jobs: the
// first series alone, so it lands first and the viewer can open; the missing
// images of every other partly stored series together in one job (an
// instance-level move cannot share a job with series-level ones); the other
// whole series dealt round robin into the remaining jobs, so each job keeps
// early series early and moves its series over one association.
func batchMoves(plan []plannedMove) [][]plannedMove {
	if len(plan) == 0 {
		return nil
	}
	jobs := [][]plannedMove{{plan[0]}}
	var whole, partial []plannedMove
	for _, move := range plan[1:] {
		if move.kind == moveMissingInstances {
			partial = append(partial, move)
			continue
		}
		whole = append(whole, move)
	}
	wholeJobs := restSeriesJobs
	if len(partial) > 0 {
		jobs = append(jobs, partial)
		wholeJobs = max(restSeriesJobs-1, 1)
	}
	groups := min(wholeJobs, len(whole))
	start := len(jobs)
	for i := 0; i < groups; i++ {
		jobs = append(jobs, nil)
	}
	for i, move := range whole {
		jobs[start+i%groups] = append(jobs[start+i%groups], move)
	}
	return jobs
}

// seriesBatchMoveRequest moves several whole series in one job.
func seriesBatchMoveRequest(moves []plannedMove, targetAet string, priority int) map[string]interface{} {
	request := seriesMoveRequest(moves[0].series, targetAet, priority)
	resources := make([]map[string]string, 0, len(moves))
	for _, move := range moves {
		resources = append(resources, map[string]string{
			"StudyInstanceUID":  move.series.StudyInstanceUID,
			"SeriesInstanceUID": move.series.SeriesInstanceUID,
		})
	}
	request["Resources"] = resources
	return request
}
