package orthanc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"api-pacs/infrastructures/providers/api/orthanc/types"
)

func series(uid, number, count string) types.QueryModalitySeriesAnswersResponse {
	return types.QueryModalitySeriesAnswersResponse{
		StudyInstanceUID:               "1.2.3",
		SeriesInstanceUID:              uid,
		SeriesNumber:                   number,
		NumberOfSeriesRelatedInstances: count,
	}
}

func TestOrderSeriesForMoveFollowsSeriesNumberWithUnnumberedLast(t *testing.T) {
	ordered := orderSeriesForMove([]types.QueryModalitySeriesAnswersResponse{
		series("s-none-a", "", "1"),
		series("s10", "10", "1"),
		series("s2", " 2 ", "1"),
		series("s-none-b", "x", "1"),
		series("s1", "1", "1"),
	})
	var got []string
	for _, s := range ordered {
		got = append(got, s.SeriesInstanceUID)
	}
	want := "s1,s2,s10,s-none-a,s-none-b"
	if strings.Join(got, ",") != want {
		t.Fatalf("order = %s, want %s", strings.Join(got, ","), want)
	}
}

func TestSeriesMovePriorityPutsViewerFirstAndFirstSeriesFirst(t *testing.T) {
	if p0, p1 := seriesMovePriority(true, 0, 3), seriesMovePriority(true, 1, 3); p0 <= p1 {
		t.Fatalf("first series priority %d should beat second %d", p0, p1)
	}
	if viewerLast, ingestionFirst := seriesMovePriority(true, 99, 100), seriesMovePriority(false, 0, 100); viewerLast <= ingestionFirst {
		t.Fatalf("viewer's last series %d should beat ingestion's first %d", viewerLast, ingestionFirst)
	}
}

func TestSeriesMoveRequestIsNotPermissive(t *testing.T) {
	body := seriesMoveRequest(series("s1", "1", "3"), "LOCAL", 7)
	if body["Permissive"] != false || body["Level"] != "Series" || body["Priority"] != 7 || body["TargetAet"] != "LOCAL" {
		t.Fatalf("unexpected move body %#v", body)
	}
}

// fakeOrthanc answers the job, resubmit, move and find calls the retrieve uses.
type fakeOrthanc struct {
	mu          sync.Mutex
	states      []string // job state per GET /jobs/{id}, last one repeats
	reads       int
	moves       []map[string]interface{}
	localImages int
}

func (f *fakeOrthanc) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/jobs/"):
			state := f.states[min(f.reads, len(f.states)-1)]
			f.reads++
			_ = json.NewEncoder(w).Encode(types.GetJobResponse{ID: strings.TrimPrefix(r.URL.Path, "/jobs/"), State: state, Progress: 100, ErrorDescription: "Peer aborted Association"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query"):
			_ = json.NewEncoder(w).Encode(types.QueryModalityResponse{ID: "q1"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/queries/q1/answers"):
			_ = json.NewEncoder(w).Encode([]types.QueryModalitySeriesAnswersResponse{
				series("late", "5", "2"), series("first", "1", "4"),
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/move"):
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.moves = append(f.moves, body)
			_ = json.NewEncoder(w).Encode(types.QueryModalityResponse{ID: "job-" + body["Resources"].([]interface{})[0].(map[string]interface{})["SeriesInstanceUID"].(string)})
		case r.Method == http.MethodPost && r.URL.Path == "/tools/find":
			instances := make([]string, f.localImages)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"Instances": instances}})
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func trackMove(jobID string, s types.QueryModalitySeriesAnswersResponse) {
	seriesMoves.track(jobID, "PACS", seriesMoveRequest(s, "LOCAL", 1), s)
}

func withTracker(t *testing.T, now *time.Time) {
	t.Helper()
	previous := seriesMoves
	seriesMoves = newMoveTracker()
	seriesMoves.now = func() time.Time { return *now }
	t.Cleanup(func() { seriesMoves = previous })
}

func TestFailedMoveIsReportedPendingThenResubmittedAfterBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Failure"}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", ""))

	job, err := api.GetJobInfo(context.Background(), "job-1")
	if err != nil || job.State != "Pending" || job.ErrorDescription != "" {
		t.Fatalf("during backoff got %+v, %v; want Pending", job, err)
	}
	if len(fake.moves) != 0 {
		t.Fatalf("moved again before backoff")
	}

	now = now.Add(seriesMoveBackoff[0])
	job, _ = api.GetJobInfo(context.Background(), "job-1")
	if job.ID != "job-1" {
		t.Fatalf("caller's job ID changed to %s", job.ID)
	}
	if job.State != "Pending" || len(fake.moves) != 1 {
		t.Fatalf("after backoff got %s with %d new moves; want Pending and 1", job.State, len(fake.moves))
	}
}

func TestMoveFailingEveryAttemptIsReportedFailed(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Failure"}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", ""))

	var job types.GetJobResponse
	for i := 0; i < 20; i++ {
		job, _ = api.GetJobInfo(context.Background(), "job-1")
		now = now.Add(time.Minute)
		if job.State == "Failure" {
			break
		}
	}
	if job.State != "Failure" {
		t.Fatalf("never reported the failure")
	}
	if len(fake.moves) != maxSeriesMoveAttempts-1 {
		t.Fatalf("new moves = %d, want %d", len(fake.moves), maxSeriesMoveAttempts-1)
	}
}

func TestSuccessWithMissingImagesIsRetriedNotReportedComplete(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Success"}, localImages: 2}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", "4"))

	job, _ := api.GetJobInfo(context.Background(), "job-1")
	if job.State != "Pending" {
		t.Fatalf("2 of 4 images reported as %s; want Pending (retry)", job.State)
	}

	fake.localImages = 4
	now = now.Add(time.Minute)
	job, _ = api.GetJobInfo(context.Background(), "job-1")
	// The waiting retry is resubmitted first; Orthanc then reports success.
	for i := 0; i < 3 && job.State != "Success"; i++ {
		job, _ = api.GetJobInfo(context.Background(), "job-1")
	}
	if job.State != "Success" {
		t.Fatalf("complete series reported as %s; want Success", job.State)
	}
}

func TestSuccessWithMissingImagesAfterLastAttemptIsFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Success"}, localImages: 1}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", "3"))

	var job types.GetJobResponse
	for i := 0; i < 20; i++ {
		job, _ = api.GetJobInfo(context.Background(), "job-1")
		now = now.Add(time.Minute)
		if job.State == "Failure" {
			break
		}
	}
	if job.State != "Failure" || !strings.Contains(job.ErrorDescription, "1 of 3") {
		t.Fatalf("got %s %q; want Failure naming 1 of 3 images", job.State, job.ErrorDescription)
	}
}

func TestUntrackedJobsAreReportedAsOrthancSaysWithoutRetry(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Failure"}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	job, _ := Init(server.URL).GetJobInfo(context.Background(), "someone-elses-job")
	if job.State != "Failure" || len(fake.moves) != 0 {
		t.Fatalf("got %s with %d new moves; want Failure and none", job.State, len(fake.moves))
	}
}

func TestRetrieveBySeriesMovesFirstSeriesFirstWithPriorities(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Pending"}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	answers, err := Init(server.URL).RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 2 || answers[0].SeriesInstanceUID != "first" || answers[0].Order != 0 || answers[1].Order != 1 {
		t.Fatalf("answers = %+v; want series 1 first", answers)
	}
	if len(fake.moves) != 2 || fake.moves[0]["Priority"].(float64) <= fake.moves[1]["Priority"].(float64) {
		t.Fatalf("moves = %+v; want first series submitted first with higher priority", fake.moves)
	}
	if fake.moves[0]["Permissive"] != false {
		t.Fatalf("move is permissive")
	}
	if _, _, expected, ok := seriesMoves.series(answers[0].ID); !ok || expected != 4 {
		t.Fatalf("first move not tracked with 4 expected images")
	}
}

func TestOldMovesAreForgotten(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	trackMove("old", series("s1", "1", "1"))
	now = now.Add(trackedMoveLifetime + time.Minute)
	trackMove("new", series("s2", "2", "1"))
	if _, _, _, ok := seriesMoves.series("old"); ok {
		t.Fatalf("old move still tracked")
	}
	if _, _, _, ok := seriesMoves.series("new"); !ok {
		t.Fatalf("new move not tracked")
	}
}
