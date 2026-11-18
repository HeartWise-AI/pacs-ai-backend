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
// Orthanc job) with backoff the next time someone reads its status, and is
// only reported as failed once the attempts are used up. Callers keep the
// first job's ID: its status is read from the latest attempt.

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

// trackedMove is a move job this process started.
type trackedMove struct {
	modalityID        string
	request           map[string]interface{} // the move, to send again
	currentJobID      string                 // Orthanc job of the latest attempt
	parts             []movePart             // the series this job moves
	expectedInstances int                    // images the PACS announced for them; 0 when unknown
	attempts          int
	retryAt           time.Time // zero unless a resubmission is waiting
	resubmitting      time.Time // when a reader started sending the next attempt; zero if none
	verified          bool      // success checked against the local copy
	trackedAt         time.Time
}

// How long a move is remembered: callers stop polling long before.
const trackedMoveLifetime = 24 * time.Hour

type moveTracker struct {
	mu    sync.Mutex
	moves map[string]*trackedMove
	now   func() time.Time
}

func newMoveTracker() *moveTracker {
	return &moveTracker{moves: map[string]*trackedMove{}, now: time.Now}
}

var seriesMoves = newMoveTracker()

func (t *moveTracker) track(jobID, modalityID string, request map[string]interface{}, series ...types.QueryModalitySeriesAnswersResponse) {
	parts := make([]movePart, 0, len(series))
	expected := 0
	for _, item := range series {
		count, _ := parseDICOMInt(item.NumberOfSeriesRelatedInstances)
		parts = append(parts, movePart{studyInstanceUID: item.StudyInstanceUID, seriesInstanceUID: item.SeriesInstanceUID, expected: count})
		expected += count
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for id, move := range t.moves {
		if now.Sub(move.trackedAt) > trackedMoveLifetime {
			delete(t.moves, id)
		}
	}
	t.moves[jobID] = &trackedMove{
		trackedAt:         now,
		modalityID:        modalityID,
		request:           request,
		currentJobID:      jobID,
		parts:             parts,
		expectedInstances: expected,
		attempts:          1,
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

// decide looks at a job's Orthanc state and says what to do with it.
// current is the Orthanc job holding the latest attempt of a move.
func (t *moveTracker) current(jobID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if move, ok := t.moves[jobID]; ok {
		return move.currentJobID
	}
	return jobID
}

// decide looks at the state of a move's latest attempt (job is that attempt;
// jobID the ID the caller holds) and says what to do with it.
func (t *moveTracker) decide(jobID string, job types.GetJobResponse) moveAction {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok {
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
		delete(t.moves, jobID)
		return moveReport
	}
	if move.retryAt.IsZero() {
		move.retryAt = t.now().Add(backoffAfter(move.attempts))
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

// retry returns what to send for the next attempt.
func (t *moveTracker) retry(jobID string) (modalityID string, request map[string]interface{}, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, found := t.moves[jobID]
	if !found {
		return "", nil, false
	}
	return move.modalityID, move.request, true
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
	}
}

// verified records the local image count after a successful move. It returns
// false when images are missing; the move then counts as failed.
func (t *moveTracker) verifiedCount(jobID string, localInstances int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, ok := t.moves[jobID]
	if !ok {
		return true
	}
	if localInstances >= move.expectedInstances {
		move.verified = true
		return true
	}
	return false
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
