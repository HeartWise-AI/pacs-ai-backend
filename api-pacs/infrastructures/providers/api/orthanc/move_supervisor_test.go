package orthanc

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"api-pacs/infrastructures/providers/api/orthanc/types"

	"github.com/jmoiron/sqlx"
)

// memoryStore is a moveStore kept in a map, as Postgres would keep it.
type memoryStore struct {
	mu      sync.Mutex
	records map[string]moveRecord
}

func newMemoryStore() *memoryStore { return &memoryStore{records: map[string]moveRecord{}} }

func (s *memoryStore) SaveMove(record moveRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.JobID] = record
	return nil
}

func (s *memoryStore) LoadMoves(since time.Time) ([]moveRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []moveRecord
	for _, record := range s.records {
		if record.TrackedAt.After(since) {
			out = append(out, record)
		}
	}
	return out, nil
}

func (s *memoryStore) DeleteMovesBefore(before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, record := range s.records {
		if record.TrackedAt.Before(before) {
			delete(s.records, id)
		}
	}
	return nil
}

// restart replaces the tracker with an empty one that loads store, as a new
// api-pacs process does.
func restart(t *testing.T, store *memoryStore, now *time.Time) {
	t.Helper()
	seriesMoves = newMoveTracker()
	seriesMoves.now = func() time.Time { return *now }
	if err := seriesMoves.useStore(store); err != nil {
		t.Fatal(err)
	}
}

func backgroundJob(state string, priority uint) types.GetJobResponse {
	return types.GetJobResponse{Type: dicomMoveJobType, State: state, Priority: priority}
}

func TestFailedMoveIsRetriedAfterARestart(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	store := newMemoryStore()
	if err := seriesMoves.useStore(store); err != nil {
		t.Fatal(err)
	}
	fake := &fakeOrthanc{states: []string{"Failure"}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", ""))
	if job, _ := api.GetJobInfo(context.Background(), "job-1"); job.State != "Pending" {
		t.Fatalf("first failure reported %s; want Pending", job.State)
	}

	restart(t, store, &now)
	now = now.Add(seriesMoveBackoff[0])
	job, err := api.GetJobInfo(context.Background(), "job-1")
	if err != nil || job.State != "Pending" || len(fake.moves) != 1 {
		t.Fatalf("after restart got %s, %v with %d new moves; want Pending and 1 (the retry)", job.State, err, len(fake.moves))
	}
	if store.records["job-1"].CurrentJobID != "job-s1" || store.records["job-1"].Attempts != 2 {
		t.Fatalf("saved move = %+v; want the new attempt recorded", store.records["job-1"])
	}
}

func TestFinishedMoveIsReportedAfterARestartWithoutOrthanc(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	store := newMemoryStore()
	_ = seriesMoves.useStore(store)
	fake := &fakeOrthanc{states: []string{"Success"}, localImages: 3}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", "3"))
	if job, _ := api.GetJobInfo(context.Background(), "job-1"); job.State != "Success" {
		t.Fatalf("complete move reported %s", job.State)
	}

	restart(t, store, &now)
	reads := fake.reads
	job, err := api.GetJobInfo(context.Background(), "job-1")
	if err != nil || job.State != "Success" || job.ID != "job-1" {
		t.Fatalf("after restart got %+v, %v; want Success for job-1", job, err)
	}
	if fake.reads != reads {
		t.Fatalf("asked Orthanc about a finished move")
	}
}

func TestSupervisorRetriesAMoveNobodyPolls(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Failure"}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", ""))

	api.superviseOnce(context.Background()) // sees the failure, starts the backoff
	now = now.Add(seriesMoveBackoff[0])
	api.superviseOnce(context.Background()) // sends the retry
	if len(fake.moves) != 1 {
		t.Fatalf("supervisor sent %d moves; want the retry", len(fake.moves))
	}
}

func TestBackgroundMovesWaitForAFreeBackgroundSlot(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &diffOrthanc{
		remote: map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}},
		local:  map[string][]string{},
		// Three background moves of another study fill the background slots;
		// a viewer move does not count.
		jobs: []types.GetJobResponse{
			backgroundJob("Running", 3), backgroundJob("Running", 2), backgroundJob("Pending", 1),
			backgroundJob("Running", interactiveMovePriority+1), backgroundJob("Success", 1),
		},
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)

	answers, err := api.RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.moves) != 0 {
		t.Fatalf("sent %d moves while the background slots are full", len(fake.moves))
	}
	if len(answers) == 0 || !strings.HasPrefix(answers[0].ID, queuedMoveIDPrefix) {
		t.Fatalf("answers = %+v; want queued move IDs", answers)
	}
	job, err := api.GetJobInfo(context.Background(), answers[0].ID)
	if err != nil || job.State != "Pending" || job.ID != answers[0].ID {
		t.Fatalf("queued move reported %+v, %v; want Pending", job, err)
	}

	// One background slot frees: only the first series is sent.
	fake.jobs = fake.jobs[1:]
	if err := api.DispatchBackgroundMoves(); err != nil {
		t.Fatal(err)
	}
	if len(fake.moves) != 1 || fake.moves[0]["Resources"].([]interface{})[0].(map[string]interface{})["SeriesInstanceUID"] != "s1" {
		t.Fatalf("moves = %+v; want only the first series", fake.moves)
	}
	if got := seriesMoves.current(answers[0].ID); got != "job-1" {
		t.Fatalf("queued move now follows %s; want job-1", got)
	}
}

func TestAtMostThreeBackgroundMovesAreSentAtOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	// Each retrieve of this 3-series study is 3 jobs; the fake lists the
	// moves it took as running.
	fake := &diffOrthanc{remote: map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}}, local: map[string][]string{}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	for i := 0; i < 2; i++ {
		if _, err := api.RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", false); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.moves) != defaultBackgroundMoveSlots {
		t.Fatalf("sent %d background moves; want %d", len(fake.moves), defaultBackgroundMoveSlots)
	}
	if waiting := len(seriesMoves.waiting()); waiting != 3 {
		t.Fatalf("%d moves waiting; want 3", waiting)
	}
}

func TestViewerRetrieveIsNeverQueued(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &diffOrthanc{
		remote: map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}},
		local:  map[string][]string{},
		jobs:   []types.GetJobResponse{backgroundJob("Running", 1), backgroundJob("Running", 1), backgroundJob("Running", 1), backgroundJob("Running", 1)},
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	answers, err := Init(server.URL).RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.moves) != len(answers) || strings.HasPrefix(answers[0].ID, queuedMoveIDPrefix) {
		t.Fatalf("viewer moves were queued: %d sent for %d answers", len(fake.moves), len(answers))
	}
}

func TestQueuedMoveOfImagesAlreadyStoredEndsWithoutMoving(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &diffOrthanc{
		remote: map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}},
		local:  map[string][]string{},
		jobs:   []types.GetJobResponse{backgroundJob("Running", 1), backgroundJob("Running", 1), backgroundJob("Running", 1)},
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	answers, err := api.RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", false)
	if err != nil {
		t.Fatal(err)
	}
	// The viewer retrieved the study meanwhile.
	fake.local = map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}}
	fake.jobs = nil
	if err := api.DispatchBackgroundMoves(); err != nil {
		t.Fatal(err)
	}
	if len(fake.moves) != 0 {
		t.Fatalf("moved %d series already stored", len(fake.moves))
	}
	for _, answer := range answers {
		if job, _ := api.GetJobInfo(context.Background(), answer.ID); job.State != "Success" {
			t.Fatalf("move %s reported %s; want Success", answer.ID, job.State)
		}
	}
}

func TestFailedBackgroundAttemptWaitsForASlotAgain(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Failure"}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	s := series("s1", "1", "")
	seriesMoves.enqueue("pacsai-move-x", "PACS", seriesMoveRequest(s, "LOCAL", 1), s)
	if err := api.DispatchBackgroundMoves(); err != nil || len(fake.moves) != 1 {
		t.Fatalf("first attempt not sent: %v, %d moves", err, len(fake.moves))
	}

	api.GetJobInfo(context.Background(), "pacsai-move-x") // failure: backoff
	now = now.Add(seriesMoveBackoff[0])
	job, _ := api.GetJobInfo(context.Background(), "pacsai-move-x")
	if job.State != "Pending" || len(fake.moves) != 1 {
		t.Fatalf("retry sent directly (%d moves, %s); want it queued", len(fake.moves), job.State)
	}
	if err := api.DispatchBackgroundMoves(); err != nil || len(fake.moves) != 2 {
		t.Fatalf("queued retry not sent when a slot is free: %v, %d moves", err, len(fake.moves))
	}
}

func TestQueuedMoveThatOrthancNeverTakesEndsAsFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Pending"}, failMoves: true}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	s := series("s1", "1", "")
	seriesMoves.enqueue("pacsai-move-x", "PACS", seriesMoveRequest(s, "LOCAL", 1), s)
	for i := 0; i < 20; i++ {
		_ = api.DispatchBackgroundMoves()
		now = now.Add(time.Minute)
	}
	job, _ := api.GetJobInfo(context.Background(), "pacsai-move-x")
	if job.State != "Failure" || !strings.Contains(job.ErrorDescription, "Cannot send the move") {
		t.Fatalf("got %+v; want Failure", job)
	}
	if len(fake.moves) != maxSeriesMoveAttempts {
		t.Fatalf("tried %d times; want %d", len(fake.moves), maxSeriesMoveAttempts)
	}
}

func TestCountBackgroundMovesIgnoresViewerAndFinishedJobs(t *testing.T) {
	jobs := []types.GetJobResponse{
		backgroundJob("Running", 0), backgroundJob("Pending", 5), backgroundJob("Retry", 1), backgroundJob("Paused", 1),
		backgroundJob("Success", 1), backgroundJob("Failure", 1),
		backgroundJob("Running", interactiveMovePriority),
		{Type: "MergeStudy", State: "Running"},
	}
	if got := countBackgroundMoves(jobs); got != 4 {
		t.Fatalf("busy = %d, want 4", got)
	}
}

func TestMoveStoreSQLCompilesAsNamedQueries(t *testing.T) {
	retry := time.Unix(2000, 0)
	record := moveRecord{JobID: "j", Request: "{}", Parts: "[]", RetryAt: &retry, TrackedAt: time.Unix(1000, 0)}
	for name, query := range map[string]struct {
		sql string
		arg interface{}
	}{
		"save":   {saveMoveSQL, record},
		"load":   {loadMovesSQL, map[string]interface{}{"since": time.Unix(0, 0)}},
		"delete": {deleteMovesSQL, map[string]interface{}{"before": time.Unix(0, 0)}},
	} {
		compiled, _, err := sqlx.Named(query.sql, query.arg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(compiled, ":text") || strings.Contains(compiled, ":jsonb") {
			t.Fatalf("%s: a cast was mangled: %s", name, compiled)
		}
	}
}

func TestMoveRecordRoundTrips(t *testing.T) {
	now := time.Unix(1000, 0)
	s := series("s1", "1", "7")
	move := newTrackedMove(now, "PACS", seriesMoveRequest(s, "LOCAL", 1004), []types.QueryModalitySeriesAnswersResponse{s})
	move.currentJobID, move.attempts, move.background, move.queued, move.queuedAt = "job-9", 2, true, true, now
	record, err := toMoveRecord("pacsai-move-1", move)
	if err != nil {
		t.Fatal(err)
	}
	back, err := fromMoveRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if back.currentJobID != "job-9" || back.attempts != 2 || !back.background || !back.queued || back.expectedInstances != 7 ||
		back.priority != 1004 || requestPriority(back.request) != 1004 || len(back.parts) != 1 || back.parts[0].seriesInstanceUID != "s1" {
		t.Fatalf("round trip = %+v", back)
	}
}
