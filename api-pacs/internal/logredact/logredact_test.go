package logredact

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// Synthetic identifiers only.
const (
	studyUID  = "1.2.826.0.1.3680043.8.498.10000000000000000001"
	seriesUID = "1.2.826.0.1.3680043.8.498.20000000000000000002"
	sopUID    = "1.2.826.0.1.3680043.8.498.30000000000000000003"
	uuidUID   = "2.25.329800735698586629295641978511506172918"
	patientID = "TEST0012345"
)

func init() { SetKeyForTest("unit-test-key") }

// leaks fails the test when out still holds one of the identifiers.
func leaks(t *testing.T, out string) {
	t.Helper()
	for _, id := range []string{studyUID, seriesUID, sopUID, uuidUID, patientID, "DOE", "19700101", "ACC998877"} {
		if strings.Contains(out, id) {
			t.Fatalf("identifier %q left in %q", id, out)
		}
	}
}

func TestHashIsShortStableAndKeyed(t *testing.T) {
	if got := UID(studyUID); !regexp.MustCompile(`^uid:[0-9a-f]{8}$`).MatchString(got) {
		t.Fatalf("UID form %q", got)
	}
	if UID(studyUID) != UID(" "+studyUID+" ") {
		t.Fatal("same UID must give the same hash")
	}
	if UID(studyUID) == UID(seriesUID) {
		t.Fatal("different UIDs gave the same hash")
	}
	before := UID(studyUID)
	SetKeyForTest("other-key")
	defer SetKeyForTest("unit-test-key")
	if UID(studyUID) == before {
		t.Fatal("hash does not depend on the key")
	}
}

func TestIsUID(t *testing.T) {
	for _, v := range []string{studyUID, uuidUID, "1.2.840.10008.5.1"} {
		if !IsUID(v) {
			t.Errorf("IsUID(%q) = false", v)
		}
	}
	for _, v := range []string{"1.0.5", "10.0.0.1", "172.18.0.5", "2.2.1", "abc", "", "1.2.3.4.x", "0.95"} {
		if IsUID(v) {
			t.Errorf("IsUID(%q) = true", v)
		}
	}
}

func TestRequestURI(t *testing.T) {
	s, se, so := UID(studyUID), UID(seriesUID), UID(sopUID)
	cases := []struct{ in, want string }{
		// OHIF prior-study search by PatientID.
		{"/proxy/orthanc/dicom-web/studies?00100020=" + patientID + "&limit=101&offset=0&fuzzymatching=false&includefield=00081030%2C00080060",
			"/proxy/orthanc/dicom-web/studies?00100020=[redacted]&limit=101&offset=0&fuzzymatching=false&includefield=00081030%2C00080060"},
		{"/dicom-web/studies?PatientName=DOE%5EJOHN&PatientBirthDate=19700101&AccessionNumber=ACC998877",
			"/dicom-web/studies?PatientName=[redacted]&PatientBirthDate=[redacted]&AccessionNumber=[redacted]"},
		{"/dicom-web/studies?00100010=DOE&00100030=19700101&00080050=ACC998877&patientid=" + patientID,
			"/dicom-web/studies?00100010=[redacted]&00100030=[redacted]&00080050=[redacted]&patientid=[redacted]"},
		// QIDO by StudyInstanceUID and tag form, single and lists.
		{"/proxy/orthanc/dicom-web/studies?limit=1&StudyInstanceUID=" + studyUID,
			"/proxy/orthanc/dicom-web/studies?limit=1&StudyInstanceUID=[" + s + "]"},
		{"/dicom-web/studies?0020000D=" + studyUID + "%2C" + seriesUID,
			"/dicom-web/studies?0020000D=[" + s + "," + se + "]"},
		{"/dicom-web/series?SeriesInstanceUID=" + seriesUID + "&SOPInstanceUID=" + sopUID + "&0020000E=" + seriesUID + "&00080018=" + sopUID,
			"/dicom-web/series?SeriesInstanceUID=[" + se + "]&SOPInstanceUID=[" + so + "]&0020000E=[" + se + "]&00080018=[" + so + "]"},
		// UIDs in the path.
		{"/proxy/orthanc/dicom-web/studies/" + studyUID + "/series/" + seriesUID + "/instances/" + sopUID + "/frames/13",
			"/proxy/orthanc/dicom-web/studies/" + s + "/series/" + se + "/instances/" + so + "/frames/13"},
		{"/proxy/orthanc/dicom-web/studies/" + studyUID + "/series?includefield=0008103E",
			"/proxy/orthanc/dicom-web/studies/" + s + "/series?includefield=0008103E"},
		{"/v1/inference/worklist/studies/" + uuidUID + "/latest-results",
			"/v1/inference/worklist/studies/" + UID(uuidUID) + "/latest-results"},
		// Orchestrator and worklist query params.
		{"/v1/orchestrator/threads?studyInstanceUID=" + studyUID + "&limit=20",
			"/v1/orchestrator/threads?studyInstanceUID=[" + s + "]&limit=20"},
		{"/v1/inference/worklist/status?limit=10&offset=0&studyInstanceUID=" + studyUID + "&studyInstanceUID=" + seriesUID,
			"/v1/inference/worklist/status?limit=10&offset=0&studyInstanceUID=[" + s + "]&studyInstanceUID=[" + se + "]"},
		// Free text search and unknown parameters holding a UID.
		{"/v1/inference/predictions?q=DOE&view=mine", "/v1/inference/predictions?q=[redacted]&view=mine"},
		{"/v1/something?study=" + studyUID, "/v1/something?study=" + s},
		// Nothing to redact.
		{"/v1/inference/model/PanEcho-1.1.0/feedback?executionId=abc", "/v1/inference/model/PanEcho-1.1.0/feedback?executionId=abc"},
		{"/v1/orthanc/jobs?jobIds=a&jobIds=b", "/v1/orthanc/jobs?jobIds=a&jobIds=b"},
		{"/v1/user/me", "/v1/user/me"},
		{"/", "/"},
		{"/dicom-web/studies?00100020=", "/dicom-web/studies?00100020="},
	}
	for _, c := range cases {
		got := RequestURI(c.in)
		if got != c.want {
			t.Errorf("RequestURI(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
		leaks(t, got)
	}
}

func TestURL(t *testing.T) {
	u, _ := url.Parse("http://orthanc:8042/dicom-web/studies/" + studyUID + "/series?00100020=" + patientID)
	got := URL(u)
	want := "http://orthanc:8042/dicom-web/studies/" + UID(studyUID) + "/series?00100020=[redacted]"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestText(t *testing.T) {
	cases := []struct{ in, want string }{
		// url.Error from the QIDO client.
		{`[dicom-web] QIDO-RS request failed: Get "http://orthanc:8042/dicom-web/studies?00100020=` + patientID + `&limit=100": context canceled`,
			`[dicom-web] QIDO-RS request failed: Get "http://orthanc:8042/dicom-web/studies?00100020=[redacted]&limit=100": context canceled`},
		// Ingestion log fields.
		{"[Ingestion service] candidate stable job_id=3MDX candidate_id=3Mh7 study_instance_uid=" + studyUID + " last_seen=x",
			"[Ingestion service] candidate stable job_id=3MDX candidate_id=3Mh7 study_instance_uid=[" + UID(studyUID) + "] last_seen=x"},
		{"patient_id=" + patientID + " patient_name=DOE accession_number=ACC998877",
			"patient_id=[redacted] patient_name=[redacted] accession_number=[redacted]"},
		{`{"PatientID":"` + patientID + `","PatientName": "DOE","StudyInstanceUID":"` + studyUID + `"}`,
			`{"PatientID":"[redacted]","PatientName": "[redacted]","StudyInstanceUID":"` + UID(studyUID) + `"}`},
		{"bare uid " + uuidUID + " end", "bare uid " + UID(uuidUID) + " end"},
		// Left alone: IPs, versions, durations, already redacted text.
		{"dial tcp 172.18.0.5:8042: connection refused", "dial tcp 172.18.0.5:8042: connection refused"},
		{"model_version=1.0.5 took 12.5ms", "model_version=1.0.5 took 12.5ms"},
		{"00100020=[redacted] study=uid:abcd1234", "00100020=[redacted] study=uid:abcd1234"},
		{"studyInstanceUID=[uid:abcd1234]", "studyInstanceUID=[uid:abcd1234]"},
	}
	for _, c := range cases {
		got := Text(c.in)
		if got != c.want {
			t.Errorf("Text(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
		leaks(t, got)
		if again := Text(got); again != got {
			t.Errorf("Text is not idempotent: %q -> %q", got, again)
		}
	}
}

func TestWriterWithStandardLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(NewWriter(&buf), "", 0)
	logger.Printf("retrieving study_instance_uid=%s for 00100020=%s", studyUID, patientID)
	leaks(t, buf.String())
	if !strings.Contains(buf.String(), UID(studyUID)) {
		t.Fatalf("hash missing: %q", buf.String())
	}
}

func TestAccessLoggerRedactsButHandlerSeesOriginal(t *testing.T) {
	var buf bytes.Buffer
	formatter := &AccessLogFormatter{middleware.DefaultLogFormatter{Logger: log.New(NewWriter(&buf), "", 0), NoColor: true}}
	target := "/proxy/orthanc/dicom-web/studies/" + studyUID + "/series?00100020=" + patientID + "&StudyInstanceUID=" + studyUID
	var seenURI, seenPatient string
	handler := middleware.RequestLogger(formatter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURI = r.RequestURI
		seenPatient = r.URL.Query().Get("00100020")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	leaks(t, out)
	if !strings.Contains(out, "/dicom-web/studies/"+UID(studyUID)+"/series?00100020=[redacted]&StudyInstanceUID=["+UID(studyUID)+"]") {
		t.Fatalf("access log line %q", out)
	}
	if !strings.Contains(out, " 200 ") {
		t.Fatalf("status missing in %q", out)
	}
	if seenURI != target || seenPatient != patientID {
		t.Fatalf("handler got a modified request: %q %q", seenURI, seenPatient)
	}
	for _, bad := range regexp.MustCompile(`00100020=[^&\[]`).FindAllString(out, -1) {
		t.Fatalf("grep check would match %q", bad)
	}
}

// Review follow-ups on #590.
func TestShortUIDsInUIDRoutes(t *testing.T) {
	short := "2.25.4242"
	cases := []struct{ in, want string }{
		{"/v1/inference/worklist/studies/" + short + "/latest-results", "/v1/inference/worklist/studies/" + UID(short) + "/latest-results"},
		{"/dicom-web/studies/1.2/series/3/instances/4.5/frames/1", "/dicom-web/studies/" + UID("1.2") + "/series/" + UID("3") + "/instances/" + UID("4.5") + "/frames/1"},
		{"/jobs/by-study/" + short + "/latest-results", "/jobs/by-study/" + UID(short) + "/latest-results"},
		{"/v1/inference/worklist/studies/" + short + "/jobs/job-1/result", "/v1/inference/worklist/studies/" + UID(short) + "/jobs/job-1/result"},
		// Route words after "studies" stay.
		{"/v1/inference/worklist/studies/reprocess-batch", "/v1/inference/worklist/studies/reprocess-batch"},
		{"/v1/orthanc/modality/studies", "/v1/orthanc/modality/studies"},
	}
	for _, c := range cases {
		if got := RequestURI(c.in); got != c.want {
			t.Errorf("RequestURI(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}

func TestTextUsesEverySensitiveParameter(t *testing.T) {
	cases := []struct{ in, want string }{
		{`Get "http://orthanc:8042/dicom-web/studies?ReferringPhysicianName=SYNTH&limit=1": context canceled`,
			`Get "http://orthanc:8042/dicom-web/studies?ReferringPhysicianName=[redacted]&limit=1": context canceled`},
		{`Get "http://x/v1/p?q=DOE&query=DOE&search=DOE&00080090=SYNTH&StudyID=77": EOF`,
			`Get "http://x/v1/p?q=[redacted]&query=[redacted]&search=[redacted]&00080090=[redacted]&StudyID=[redacted]": EOF`},
		{`Get "http://orthanc:8042/dicom-web/studies?00100020=` + patientID + `": EOF`,
			`Get "http://orthanc:8042/dicom-web/studies?00100020=[redacted]": EOF`},
		{"series_instance_uids=2.25.4242", "series_instance_uids=[" + UID("2.25.4242") + "]"},
		{`{"ReferringPhysicianName":"SYNTH"}`, `{"ReferringPhysicianName":"[redacted]"}`},
		// Unrelated name=value pairs stay.
		{"job_id=3MDX status=STOPPED model_version=1.0.5 url=http://orthanc:8042/x", "job_id=3MDX status=STOPPED model_version=1.0.5 url=http://orthanc:8042/x"},
	}
	for _, c := range cases {
		got := Text(c.in)
		if got != c.want {
			t.Errorf("Text(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "SYNTH") || strings.Contains(got, "DOE") {
			t.Errorf("leak in %q", got)
		}
		if again := Text(got); again != got {
			t.Errorf("Text is not idempotent: %q -> %q", got, again)
		}
	}
}

func TestTextKeepsWholeSensitiveValues(t *testing.T) {
	cases := []struct{ in, want string }{
		{"patient_id=SITE/TEST0012345 next=1", "patient_id=[redacted] next=1"},
		{"PatientName=DOE?X=1", "PatientName=[redacted]"},
		{`Get "http://orthanc:8042/dicom-web/studies?StudyInstanceUID=1.2%2C2.3&limit=1": EOF`,
			`Get "http://orthanc:8042/dicom-web/studies?StudyInstanceUID=[` + UID("1.2") + "," + UID("2.3") + `]&limit=1": EOF`},
		{"url=http://orthanc:8042/dicom-web/studies?00100020=" + patientID, "url=http://orthanc:8042/dicom-web/studies?00100020=[redacted]"},
	}
	for _, c := range cases {
		got := Text(c.in)
		if got != c.want {
			t.Errorf("Text(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "TEST0012345") || strings.Contains(got, "DOE") {
			t.Errorf("leak in %q", got)
		}
		if again := Text(got); again != got {
			t.Errorf("Text is not idempotent: %q -> %q", got, again)
		}
	}
}

func TestTextKeepsAllDigitHashes(t *testing.T) {
	for _, in := range []string{"/studies/uid:28316730/series", "study uid:12345678 done", "studyInstanceUID=[uid:28316730]"} {
		if got := Text(in); got != in {
			t.Errorf("Text(%q) = %q", in, got)
		}
	}
	// Hashes of real UIDs pass through Text unchanged, whatever their digits.
	for i := 0; i < 2000; i++ {
		u := UID("1.2.826.0.1.3680043.8.498." + strconv.Itoa(i))
		if got := Text("/studies/" + u); got != "/studies/"+u {
			t.Fatalf("hash %q changed to %q", u, got)
		}
	}
}

func TestTextIsLinearOnRepeatedSeparators(t *testing.T) {
	line := "/x?x=" + strings.Repeat("a=", 64*1024)
	start := time.Now()
	Text(line)
	RequestURI(line)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("128 KB line took %v", elapsed)
	}
	// Sensitive names with long values stay linear too.
	line = strings.Repeat("patient_id=a/a?a ", 8*1024)
	start = time.Now()
	if got := Text(line); strings.Contains(got, "a/a") {
		t.Fatalf("leak: %q", got[:64])
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("sensitive line took %v", elapsed)
	}
}
