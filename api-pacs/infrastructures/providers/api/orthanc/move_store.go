package orthanc

import (
	"encoding/json"
	"log"
	"time"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
)

// moveStore keeps the tracked moves across api-pacs restarts.
type moveStore interface {
	SaveMove(record moveRecord) error
	LoadMoves(since time.Time) ([]moveRecord, error)
	DeleteMovesBefore(before time.Time) error
}

// moveRecord is a tracked move as saved in orthanc_tracked_moves.
type moveRecord struct {
	JobID             string     `db:"job_id"`
	ModalityID        string     `db:"modality_id"`
	Request           string     `db:"request"`
	CurrentJobID      string     `db:"current_job_id"`
	Parts             string     `db:"parts"`
	ExpectedInstances int        `db:"expected_instances"`
	Attempts          int        `db:"attempts"`
	RetryAt           *time.Time `db:"retry_at"`
	Verified          bool       `db:"verified"`
	Background        bool       `db:"background"`
	Queued            bool       `db:"queued"`
	QueuedAt          *time.Time `db:"queued_at"`
	Priority          int        `db:"priority"`
	Done              bool       `db:"done"`
	FinalState        string     `db:"final_state"`
	FinalError        string     `db:"final_error"`
	CompletionTime    string     `db:"completion_time"`
	TrackedAt         time.Time  `db:"tracked_at"`
}

type movePartRecord struct {
	StudyInstanceUID  string `json:"studyInstanceUID"`
	SeriesInstanceUID string `json:"seriesInstanceUID"`
	Expected          int    `json:"expected"`
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func toMoveRecord(jobID string, move *trackedMove) (moveRecord, error) {
	request, err := json.Marshal(move.request)
	if err != nil {
		return moveRecord{}, err
	}
	parts := make([]movePartRecord, 0, len(move.parts))
	for _, part := range move.parts {
		parts = append(parts, movePartRecord{StudyInstanceUID: part.studyInstanceUID, SeriesInstanceUID: part.seriesInstanceUID, Expected: part.expected})
	}
	encodedParts, err := json.Marshal(parts)
	if err != nil {
		return moveRecord{}, err
	}
	return moveRecord{
		JobID:             jobID,
		ModalityID:        move.modalityID,
		Request:           string(request),
		CurrentJobID:      move.currentJobID,
		Parts:             string(encodedParts),
		ExpectedInstances: move.expectedInstances,
		Attempts:          move.attempts,
		RetryAt:           optionalTime(move.retryAt),
		Verified:          move.verified,
		Background:        move.background,
		Queued:            move.queued,
		QueuedAt:          optionalTime(move.queuedAt),
		Priority:          move.priority,
		Done:              move.done,
		FinalState:        move.finalState,
		FinalError:        move.finalError,
		CompletionTime:    move.completionTime,
		TrackedAt:         move.trackedAt,
	}, nil
}

func fromMoveRecord(record moveRecord) (*trackedMove, error) {
	var request map[string]interface{}
	if err := json.Unmarshal([]byte(record.Request), &request); err != nil {
		return nil, err
	}
	var parts []movePartRecord
	if err := json.Unmarshal([]byte(record.Parts), &parts); err != nil {
		return nil, err
	}
	move := &trackedMove{
		modalityID:        record.ModalityID,
		request:           request,
		currentJobID:      record.CurrentJobID,
		expectedInstances: record.ExpectedInstances,
		attempts:          record.Attempts,
		retryAt:           timeOrZero(record.RetryAt),
		verified:          record.Verified,
		trackedAt:         record.TrackedAt,
		background:        record.Background,
		queued:            record.Queued,
		queuedAt:          timeOrZero(record.QueuedAt),
		priority:          record.Priority,
		done:              record.Done,
		finalState:        record.FinalState,
		finalError:        record.FinalError,
		completionTime:    record.CompletionTime,
	}
	for _, part := range parts {
		move.parts = append(move.parts, movePart{studyInstanceUID: part.StudyInstanceUID, seriesInstanceUID: part.SeriesInstanceUID, expected: part.Expected})
	}
	return move, nil
}

// saveLocked writes a move to the store; t.mu is held. A failed write is
// logged: the move goes on in memory.
func (t *moveTracker) saveLocked(jobID string, move *trackedMove) {
	if t.store == nil {
		return
	}
	record, err := toMoveRecord(jobID, move)
	if err == nil {
		err = t.store.SaveMove(record)
	}
	if err != nil {
		log.Printf("[retrieve] cannot save move job %s: %v", jobID, err)
	}
}

// useStore makes the tracker save its moves in store and loads the moves
// saved in the last trackedMoveLifetime.
func (t *moveTracker) useStore(store moveStore) error {
	records, err := store.LoadMoves(t.now().Add(-trackedMoveLifetime))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.store = store
	if err != nil {
		return err
	}
	for _, record := range records {
		move, err := fromMoveRecord(record)
		if err != nil {
			log.Printf("[retrieve] cannot load saved move job %s: %v", record.JobID, err)
			continue
		}
		if _, exists := t.moves[record.JobID]; !exists {
			t.moves[record.JobID] = move
		}
	}
	return nil
}

// PostgresMoveStore saves tracked moves in orthanc_tracked_moves (migration
// 000015).
type PostgresMoveStore struct {
	DB postgresqlTypes.PostgresSQLDBHandlerInterface
}

// No "::" casts: the handler's named queries would turn them into ":".
const saveMoveSQL = `
INSERT INTO orthanc_tracked_moves (
    job_id, modality_id, request, current_job_id, parts, expected_instances,
    attempts, retry_at, verified, background, queued, queued_at, priority,
    done, final_state, final_error, completion_time, tracked_at, updated_at
) VALUES (
    :job_id, :modality_id, CAST(:request AS jsonb), :current_job_id, CAST(:parts AS jsonb), :expected_instances,
    :attempts, :retry_at, :verified, :background, :queued, :queued_at, :priority,
    :done, :final_state, :final_error, :completion_time, :tracked_at, now()
)
ON CONFLICT (job_id) DO UPDATE SET
    current_job_id = EXCLUDED.current_job_id,
    attempts = EXCLUDED.attempts,
    retry_at = EXCLUDED.retry_at,
    verified = EXCLUDED.verified,
    queued = EXCLUDED.queued,
    queued_at = EXCLUDED.queued_at,
    done = EXCLUDED.done,
    final_state = EXCLUDED.final_state,
    final_error = EXCLUDED.final_error,
    completion_time = EXCLUDED.completion_time,
    updated_at = now()`

const loadMovesSQL = `
SELECT job_id, modality_id, CAST(request AS text) AS request, current_job_id,
       CAST(parts AS text) AS parts, expected_instances, attempts, retry_at,
       verified, background, queued, queued_at, priority, done, final_state,
       final_error, completion_time, tracked_at
FROM orthanc_tracked_moves
WHERE tracked_at > :since`

const deleteMovesSQL = `DELETE FROM orthanc_tracked_moves WHERE tracked_at < :before`

func (s PostgresMoveStore) SaveMove(record moveRecord) error {
	_, err := s.DB.Execute(saveMoveSQL, record)
	return err
}

func (s PostgresMoveStore) LoadMoves(since time.Time) ([]moveRecord, error) {
	var records []moveRecord
	err := s.DB.Query(loadMovesSQL, map[string]interface{}{"since": since}, &records)
	return records, err
}

func (s PostgresMoveStore) DeleteMovesBefore(before time.Time) error {
	_, err := s.DB.Execute(deleteMovesSQL, map[string]interface{}{"before": before})
	return err
}
