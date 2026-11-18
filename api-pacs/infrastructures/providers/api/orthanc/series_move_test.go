package orthanc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
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
	failMoves   bool // answer every move with an error
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
			if f.failMoves {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
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
	if _, expected, ok := seriesMoves.parts(answers[0].ID); !ok || expected != 4 {
		t.Fatalf("first move not tracked with 4 expected images")
	}
}

func TestOldMovesAreForgotten(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	trackMove("old", series("s1", "1", "1"))
	now = now.Add(trackedMoveLifetime + time.Minute)
	trackMove("new", series("s2", "2", "1"))
	if _, _, ok := seriesMoves.parts("old"); ok {
		t.Fatalf("old move still tracked")
	}
	if _, _, ok := seriesMoves.parts("new"); !ok {
		t.Fatalf("new move not tracked")
	}
}

func TestPlanStudyMovesSkipsCompleteSeriesAndCompletesPartialOnes(t *testing.T) {
	remote := []types.QueryModalitySeriesAnswersResponse{
		series("new", "1", "3"),
		series("partial", "2", "5"),
		series("complete", "3", "4"),
		series("stored-no-count", "4", ""),
		series("empty-local", "5", "2"),
	}
	local := map[string]localSeries{
		"partial":         {orthancID: "o-partial", instances: 2},
		"complete":        {orthancID: "o-complete", instances: 4},
		"stored-no-count": {orthancID: "o-x", instances: 1},
		"empty-local":     {orthancID: "o-e", instances: 0},
	}
	plan := planStudyMoves(remote, local)
	var got []string
	for _, move := range plan {
		kind := "whole"
		if move.kind == moveMissingInstances {
			kind = "missing"
		}
		got = append(got, move.series.SeriesInstanceUID+":"+kind)
	}
	want := "new:whole,partial:missing,empty-local:whole"
	if strings.Join(got, ",") != want {
		t.Fatalf("plan = %s, want %s", strings.Join(got, ","), want)
	}
}

func TestMissingInstancesKeepsPACSOrderWithoutDuplicates(t *testing.T) {
	got := missingInstances([]string{"a", "b", "c", "b", "", "d"}, []string{"a", "d"})
	if strings.Join(got, ",") != "b,c" {
		t.Fatalf("missing = %v, want b,c", got)
	}
}

func TestInstancesMoveRequestMovesOnlyTheGivenImagesInOneJob(t *testing.T) {
	body := instancesMoveRequest(series("s1", "1", "5"), []string{"x", "y"}, "LOCAL", 1003)
	resources := body["Resources"].([]map[string]string)
	if body["Level"] != "Instance" || len(resources) != 2 || resources[1]["SOPInstanceUID"] != "y" || body["Priority"] != 1003 || body["Permissive"] != false {
		t.Fatalf("unexpected body %#v", body)
	}
}

// diffOrthanc is a fake Orthanc with a PACS study and a partly stored copy.
type diffOrthanc struct {
	mu     sync.Mutex
	remote map[string][]string // PACS: series -> images
	local  map[string][]string // local store: series -> images
	moves  []map[string]interface{}
}

func (f *diffOrthanc) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body map[string]interface{}
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/query"):
			level := body["Level"].(string)
			query := body["Query"].(map[string]interface{})
			id := "q-series"
			if level == "Instance" {
				id = "q-instances-" + query["SeriesInstanceUID"].(string)
			}
			_ = json.NewEncoder(w).Encode(types.QueryModalityResponse{ID: id})
		case r.URL.Path == "/queries/q-series/answers":
			var answers []types.QueryModalitySeriesAnswersResponse
			for i, uid := range []string{"s1", "s2", "s3"} {
				answers = append(answers, series(uid, strconv.Itoa(i+1), strconv.Itoa(len(f.remote[uid]))))
			}
			_ = json.NewEncoder(w).Encode(answers)
		case strings.HasPrefix(r.URL.Path, "/queries/q-instances-"):
			uid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/queries/q-instances-"), "/answers")
			var answers []map[string]string
			for _, sop := range f.remote[uid] {
				answers = append(answers, map[string]string{"SOPInstanceUID": sop})
			}
			_ = json.NewEncoder(w).Encode(answers)
		case r.URL.Path == "/tools/find":
			var out []map[string]interface{}
			for uid, sops := range f.local {
				out = append(out, map[string]interface{}{
					"ID": "o-" + uid, "Instances": sops,
					"MainDicomTags": map[string]string{"SeriesInstanceUID": uid},
				})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(r.URL.Path, "/series/o-") && strings.HasSuffix(r.URL.Path, "/instances"):
			uid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/series/o-"), "/instances")
			var out []map[string]interface{}
			for _, sop := range f.local[uid] {
				out = append(out, map[string]interface{}{"MainDicomTags": map[string]string{"SOPInstanceUID": sop}})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasSuffix(r.URL.Path, "/move"):
			f.moves = append(f.moves, body)
			_ = json.NewEncoder(w).Encode(types.QueryModalityResponse{ID: "job-" + strconv.Itoa(len(f.moves))})
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestRetrieveMovesOnlyWhatIsMissing(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &diffOrthanc{
		remote: map[string][]string{"s1": {"a", "b"}, "s2": {"c", "d", "e"}, "s3": {"f"}},
		// s1 complete, s2 partly stored, s3 not stored
		local: map[string][]string{"s1": {"a", "b"}, "s2": {"c"}},
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	answers, err := Init(server.URL).RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.moves) != 2 || len(answers) != 2 {
		t.Fatalf("moves = %+v; want 2 (s1 skipped)", fake.moves)
	}
	first, second := fake.moves[0], fake.moves[1]
	if first["Level"] != "Instance" {
		t.Fatalf("partly stored s2 moved as %v; want only its images", first["Level"])
	}
	var sops []string
	for _, resource := range first["Resources"].([]interface{}) {
		sops = append(sops, resource.(map[string]interface{})["SOPInstanceUID"].(string))
	}
	if strings.Join(sops, ",") != "d,e" {
		t.Fatalf("s2 images moved = %v; want d,e", sops)
	}
	if second["Level"] != "Series" || second["Resources"].([]interface{})[0].(map[string]interface{})["SeriesInstanceUID"] != "s3" {
		t.Fatalf("second move = %+v; want s3 whole", second)
	}
	if answers[0].Order != 0 || answers[1].Order != 1 {
		t.Fatalf("orders = %d,%d; want 0,1", answers[0].Order, answers[1].Order)
	}
}

func TestRetrieveOfACompleteStudyMovesNothing(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &diffOrthanc{
		remote: map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}},
		local:  map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}},
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	_, err := Init(server.URL).RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", true)
	if err == nil || err.Error() != apiError.DuplicateRecord {
		t.Fatalf("err = %v; want DuplicateRecord", err)
	}
	if len(fake.moves) != 0 {
		t.Fatalf("moved %d series of a complete study", len(fake.moves))
	}
}

func TestViewerRetrieveGoesAheadOfIngestion(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	remote := map[string][]string{"s1": {"a"}, "s2": {"b"}, "s3": {"c"}}
	ingestion := &diffOrthanc{remote: remote, local: map[string][]string{}}
	viewer := &diffOrthanc{remote: remote, local: map[string][]string{}}
	for _, f := range []*diffOrthanc{ingestion, viewer} {
		server := httptest.NewServer(f.handler(t))
		defer server.Close()
		_, err := Init(server.URL).RetrieveModalityStudyBySeries(context.Background(), "PACS", "LOCAL", "1.2.3", f == viewer)
		if err != nil {
			t.Fatal(err)
		}
	}
	lowestViewer := viewer.moves[len(viewer.moves)-1]["Priority"].(float64)
	highestIngestion := ingestion.moves[0]["Priority"].(float64)
	if lowestViewer <= highestIngestion {
		t.Fatalf("viewer priority %v not above ingestion %v", lowestViewer, highestIngestion)
	}
}

func TestBatchMovesPutsFirstSeriesAloneAndSharesTheRest(t *testing.T) {
	var plan []plannedMove
	for i := 1; i <= 8; i++ {
		plan = append(plan, plannedMove{series: series("s"+strconv.Itoa(i), strconv.Itoa(i), "1")})
	}
	plan[4].kind = moveMissingInstances // s5 partly stored
	jobs := batchMoves(plan)
	var got []string
	for _, job := range jobs {
		var uids []string
		for _, move := range job {
			uids = append(uids, move.series.SeriesInstanceUID)
		}
		got = append(got, strings.Join(uids, "+"))
	}
	want := "s1|s5|s2+s6|s3+s7|s4+s8"
	if strings.Join(got, "|") != want {
		t.Fatalf("jobs = %s, want %s", strings.Join(got, "|"), want)
	}
	if len(batchMoves(plan[:2])) != 2 || batchMoves(nil) != nil {
		t.Fatalf("small plans not batched as expected")
	}
}

func TestBatchedJobIsCheckedAgainstEveryOneOfItsSeries(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Success"}, localImages: 2}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	request := map[string]interface{}{"Level": "Series"}
	// Two series of 2 images each: the fake has 2 images of each locally.
	seriesMoves.track("job-batch", "PACS", request, series("a", "2", "2"), series("b", "3", "2"))
	job, _ := Init(server.URL).GetJobInfo(context.Background(), "job-batch")
	if job.State != "Success" {
		t.Fatalf("complete batch reported %s", job.State)
	}
	seriesMoves.track("job-short", "PACS", request, series("a", "2", "2"), series("b", "3", "3"))
	job, _ = Init(server.URL).GetJobInfo(context.Background(), "job-short")
	if job.State != "Pending" {
		t.Fatalf("batch with a short series reported %s; want Pending (retry)", job.State)
	}
}

func TestOnlyOneReaderResubmitsAMove(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	trackMove("job-1", series("s1", "1", ""))
	failure := types.GetJobResponse{State: string(types.JobFailure)}

	if got := seriesMoves.decide("job-1", failure); got != moveWaitRetry {
		t.Fatalf("first failure: %v, want wait", got)
	}
	now = now.Add(seriesMoveBackoff[0])
	if got := seriesMoves.decide("job-1", failure); got != moveResubmit {
		t.Fatalf("after backoff: %v, want resubmit", got)
	}
	// The viewer and ingestion read the same job at the same time.
	if got := seriesMoves.decide("job-1", failure); got != moveWaitRetry {
		t.Fatalf("second reader: %v, want wait while the first one resubmits", got)
	}
	seriesMoves.resubmitted("job-1", "job-1b")
	if got := seriesMoves.current("job-1"); got != "job-1b" {
		t.Fatalf("current attempt %s, want job-1b", got)
	}
}

func TestStaleResubmitClaimExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	trackMove("job-1", series("s1", "1", ""))
	failure := types.GetJobResponse{State: string(types.JobFailure)}
	seriesMoves.decide("job-1", failure)
	now = now.Add(seriesMoveBackoff[0])
	if got := seriesMoves.decide("job-1", failure); got != moveResubmit {
		t.Fatalf("after backoff: %v, want resubmit", got)
	}
	now = now.Add(resubmitClaimLifetime)
	if got := seriesMoves.decide("job-1", failure); got != moveResubmit {
		t.Fatalf("after a stale claim: %v, want resubmit", got)
	}
}

func TestFailedResubmitBacksOffAndEndsAsFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	withTracker(t, &now)
	fake := &fakeOrthanc{states: []string{"Failure"}, failMoves: true}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	api := Init(server.URL)
	trackMove("job-1", series("s1", "1", ""))

	// Poll every second for two minutes, as the viewer does, while the PACS
	// refuses every new move.
	var job types.GetJobResponse
	for i := 0; i < 120; i++ {
		job, _ = api.GetJobInfo(context.Background(), "job-1")
		if job.State == "Failure" {
			break
		}
		now = now.Add(time.Second)
	}
	if job.State != "Failure" {
		t.Fatalf("never reported the failure; %d moves sent", len(fake.moves))
	}
	if len(fake.moves) != maxSeriesMoveAttempts-1 {
		t.Fatalf("moves sent = %d, want %d (one per attempt, with backoff)", len(fake.moves), maxSeriesMoveAttempts-1)
	}
}
