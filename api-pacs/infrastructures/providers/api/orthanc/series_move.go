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

// trackedMove is a series move this process started.
type trackedMove struct {
	modalityID        string
	request           map[string]interface{} // the move, to send again
	currentJobID      string                 // Orthanc job of the latest attempt
	studyInstanceUID  string
	seriesInstanceUID string
	expectedInstances int // 0 when the PACS did not say
	attempts          int
	retryAt           time.Time // zero unless a resubmission is waiting
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

func (t *moveTracker) track(jobID, modalityID string, request map[string]interface{}, series types.QueryModalitySeriesAnswersResponse) {
	expected, _ := parseDICOMInt(series.NumberOfSeriesRelatedInstances)
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
		studyInstanceUID:  series.StudyInstanceUID,
		seriesInstanceUID: series.SeriesInstanceUID,
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
	if move.attempts >= maxSeriesMoveAttempts {
		delete(t.moves, jobID)
		return moveReport
	}
	if move.retryAt.IsZero() {
		move.retryAt = t.now().Add(seriesMoveBackoff[move.attempts-1])
	}
	if t.now().Before(move.retryAt) {
		return moveWaitRetry
	}
	return moveResubmit
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
		move.currentJobID = newJobID
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

func (t *moveTracker) series(jobID string) (studyInstanceUID, seriesInstanceUID string, expected int, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	move, found := t.moves[jobID]
	if !found {
		return "", "", 0, false
	}
	return move.studyInstanceUID, move.seriesInstanceUID, move.expectedInstances, true
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
