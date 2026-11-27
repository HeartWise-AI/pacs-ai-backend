package orthanc

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/go-chi/chi/v5"

	orthancTypes "api-pacs/infrastructures/providers/api/orthanc/types"
	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/orthanc/infrastructure/service"
	rest "api-pacs/module/orthanc/interfaces/http/rest"
)

// orthancRoute is the path Orthanc 1.12.5 routes after CivetWeb 1.14
// (civetweb.c handle_request: split on '?', mg_url_decode with
// decode_url default "yes", then remove_dot_segments).
func orthancRoute(requestTarget string) string {
	path, _, _ := strings.Cut(requestTarget, "?")
	return removeDotSegments(mgURLDecode(path))
}

func mgURLDecode(src string) string {
	s := []byte(src)
	dst := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if i+2 < len(s) && s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
			dst = append(dst, byte(hexVal(s[i+1])<<4|hexVal(s[i+2])))
			i += 3
			continue
		}
		dst = append(dst, s[i])
		i++
	}
	return string(dst)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	default:
		return int(unicode.ToLower(rune(c)) - 'W')
	}
}

func removeDotSegments(s string) string {
	buf := make([]byte, len(s)+1)
	copy(buf, s)
	n := len(s)
	for i := 0; i < n; i++ {
		if buf[i] == '\\' {
			buf[i] = '/'
		}
	}
	in := 0
	outEnd := 0
	for in < n && buf[in] != 0 {
		rem := buf[in:n]
		switch {
		case bytes.HasPrefix(rem, []byte("../")):
			in += 3
		case bytes.HasPrefix(rem, []byte("./")):
			in += 2
		case bytes.HasPrefix(rem, []byte("/./")):
			in += 2
		case bytes.Equal(rem, []byte("/.")):
			n = in + 1
			buf[in+1] = 0
		case bytes.HasPrefix(rem, []byte("/../")):
			in += 3
			if outEnd != 0 {
				for {
					outEnd--
					if outEnd == 0 || buf[outEnd] == '/' {
						break
					}
				}
			}
		case bytes.Equal(rem, []byte("/..")):
			n = in + 1
			buf[in+1] = 0
			if outEnd != 0 {
				for {
					outEnd--
					if outEnd == 0 || buf[outEnd] == '/' {
						break
					}
				}
			}
		case bytes.Equal(rem, []byte(".")) || bytes.Equal(rem, []byte("..")):
			n = in
			buf[in] = 0
		default:
			for {
				buf[outEnd] = buf[in]
				outEnd++
				in++
				if in >= n || buf[in] == 0 || buf[in] == '/' {
					break
				}
			}
		}
	}
	n = outEnd
	buf[n] = 0
	in = 0
	outEnd = 0
	for in < n && buf[in] != 0 {
		if buf[in] == '.' {
			ahead := in
			for ahead < n && buf[ahead] == '.' {
				ahead++
			}
			// The C loop increments once before testing, so a single dot is included.
			if ahead == in {
				ahead++
			}
			if ahead < n && buf[ahead] == '/' {
				in = ahead
				if outEnd != 0 && buf[outEnd-1] == '/' {
					outEnd--
				}
			} else if ahead >= n || buf[ahead] == 0 {
				in = n
			} else {
				for in != ahead {
					buf[outEnd] = '.'
					outEnd++
					in++
				}
			}
		} else if buf[in] == '/' {
			buf[outEnd] = '/'
			outEnd++
			in++
			for in < n && buf[in] == '/' {
				in++
			}
		} else {
			buf[outEnd] = buf[in]
			outEnd++
			in++
		}
	}
	return string(buf[:outEnd])
}

type routedCall struct {
	Method     string
	RawTarget  string
	RoutedPath string
}

type orthancLog struct {
	mu    sync.Mutex
	calls []routedCall
}

func (l *orthancLog) add(call routedCall) {
	l.mu.Lock()
	l.calls = append(l.calls, call)
	l.mu.Unlock()
}

func (l *orthancLog) reset() {
	l.mu.Lock()
	l.calls = nil
	l.mu.Unlock()
}

func (l *orthancLog) snapshot() []routedCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]routedCall, len(l.calls))
	copy(out, l.calls)
	return out
}

func startOrthancListener(t *testing.T) (string, *orthancLog) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	log := &orthancLog{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
				log.add(routedCall{
					Method:     req.Method,
					RawTarget:  req.RequestURI,
					RoutedPath: orthancRoute(req.RequestURI),
				})
				_, _ = io.WriteString(conn, "HTTP/1.1 500 Error\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
			}(conn)
		}
	}()
	return "http://" + ln.Addr().String(), log
}

func singleSegment(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/?#")
}

func modalityPath(path, suffix string) bool {
	rest, ok := strings.CutPrefix(path, "/modalities/")
	if !ok {
		return false
	}
	if suffix == "" {
		return singleSegment(rest)
	}
	id, ok := strings.CutSuffix(rest, suffix)
	return ok && singleSegment(id)
}

func requireModality(t *testing.T, calls []routedCall, method, suffix string) {
	t.Helper()
	for _, call := range calls {
		if call.Method != method || !modalityPath(call.RoutedPath, suffix) {
			t.Fatalf("Orthanc routes %s %s (request-target %s)", call.Method, call.RoutedPath, call.RawTarget)
		}
	}
}

func signedRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	ctx := context.WithValue(req.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	return req.WithContext(ctx)
}

func queryController(base string) *rest.OrthancQueryController {
	api := Init(base)
	svc := &service.OrthancQueryService{OrthancAPIInterface: api}
	return &rest.OrthancQueryController{OrthancQueryServiceInterface: svc}
}

func commandController(base string) *rest.OrthancCommandController {
	api := Init(base)
	svc := &service.OrthancCommandService{OrthancAPIInterface: api}
	return &rest.OrthancCommandController{OrthancCommandServiceInterface: svc}
}

func TestCivetwebRouteMatchesOrthanc114(t *testing.T) {
	cases := map[string]string{
		"/modalities/x/../../tools/shutdown?/query":                    "/tools/shutdown",
		"/jobs/../tools/shutdown":                                      "/tools/shutdown",
		"/jobs/../tools/shutdown?":                                     "/tools/shutdown",
		"/modalities/PACS_1/query":                                     "/modalities/PACS_1/query",
		"/modalities/x%2F..%2F..%2Ftools%2Fshutdown%3F/query":          "/tools/shutdown?/query",
		"/modalities/legit?/query":                                     "/modalities/legit",
		"/modalities/x/../../tools/shutdown/move":                      "/tools/shutdown/move",
		"/modalities/x/../../tools/shutdown?/move":                     "/tools/shutdown",
		"/jobs/11111111-1111-1111-1111-111111111111":                   "/jobs/11111111-1111-1111-1111-111111111111",
		"/modalities/x%2F..%2F..%2Ftools%2Fshutdown%3F/store-straight": "/tools/shutdown?/store-straight",
		"/modalities/x/../../tools/shutdown?/echo":                     "/tools/shutdown",
	}
	for target, want := range cases {
		if got := orthancRoute(target); got != want {
			t.Errorf("orthancRoute(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestBenignModalitySearchStaysOnQuery(t *testing.T) {
	base, calls := startOrthancListener(t)
	body := bytes.NewBufferString(`{"modalityId":"PACS_1","studyInstanceUID":"1.2.3"}`)
	rec := httptest.NewRecorder()
	queryController(base).FindModalityStudies(rec, signedRequest(http.MethodPost, "/v1/orthanc/modality/studies", body))
	got := calls.snapshot()
	if len(got) != 1 || got[0].Method != http.MethodPost || got[0].RoutedPath != "/modalities/PACS_1/query" {
		t.Fatalf("calls = %+v", got)
	}
}

func TestFindModalityStudiesDoesNotRetargetOrthanc(t *testing.T) {
	base, calls := startOrthancListener(t)
	payloads := []string{
		`{"modalityId":"x/../../tools/shutdown?","studyInstanceUID":"1.2.3"}`,
		`{"modalityId":"legit?","studyInstanceUID":"1.2.3"}`,
	}
	for _, payload := range payloads {
		t.Run(payload, func(t *testing.T) {
			calls.reset()
			rec := httptest.NewRecorder()
			queryController(base).FindModalityStudies(rec, signedRequest(http.MethodPost, "/v1/orthanc/modality/studies", strings.NewReader(payload)))
			requireModality(t, calls.snapshot(), http.MethodPost, "/query")
		})
	}
}

func TestGetJobsInfoDoesNotRetargetOrthanc(t *testing.T) {
	base, calls := startOrthancListener(t)
	targets := []string{
		"/v1/orthanc/jobs?jobIds=../tools/shutdown",
		"/v1/orthanc/jobs?jobIds=../tools/shutdown?",
		"/v1/orthanc/jobs?jobIds=../tools/reset",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			calls.reset()
			rec := httptest.NewRecorder()
			queryController(base).GetJobsInfo(rec, signedRequest(http.MethodGet, target, nil))
			for _, call := range calls.snapshot() {
				id := strings.TrimPrefix(call.RoutedPath, "/jobs/")
				if call.Method != http.MethodGet || !singleSegment(id) || call.RoutedPath != "/jobs/"+id {
					t.Fatalf("Orthanc routes %s %s (request-target %s)", call.Method, call.RoutedPath, call.RawTarget)
				}
			}
		})
	}
}

func TestRetrieveModalityStudyDoesNotRetargetOrthanc(t *testing.T) {
	base, calls := startOrthancListener(t)
	body := bytes.NewBufferString(`{"modalityId":"x/../../tools/shutdown?","studyInstanceUID":"1.2.3"}`)
	rec := httptest.NewRecorder()
	commandController(base).RetrieveModalityStudy(rec, signedRequest(http.MethodPost, "/v1/orthanc/modality/retrieve", body))
	for _, call := range calls.snapshot() {
		if call.Method != http.MethodPost || (!modalityPath(call.RoutedPath, "/query") && !modalityPath(call.RoutedPath, "/move")) {
			t.Fatalf("Orthanc routes %s %s (request-target %s)", call.Method, call.RoutedPath, call.RawTarget)
		}
	}
}

func TestSeriesMoveDoesNotRetargetOrthanc(t *testing.T) {
	base, calls := startOrthancListener(t)
	api := Init(base)
	_, _ = api.submitSeriesMove("x/../../tools/shutdown?", map[string]interface{}{"Level": "Series"})
	requireModality(t, calls.snapshot(), http.MethodPost, "/move")
}

func TestModalityMutationsDoNotRetargetOrthanc(t *testing.T) {
	base, calls := startOrthancListener(t)
	api := Init(base)
	id := "x/../../tools/shutdown?"
	ctx := context.Background()
	_ = api.DeleteDICOMModality(ctx, id)
	_, _ = api.StraightDICOMStoreSCU(ctx, id, []byte("dicom"))
	_ = api.TriggerDICOMEchoSCU(ctx, id)
	_ = api.UpdateDICOMModality(ctx, id, orthancTypes.UpdateDICOMModalityRequest{AET: "A", Host: "h", Port: 1})
	for _, call := range calls.snapshot() {
		ok := false
		switch call.Method {
		case http.MethodDelete, http.MethodPut:
			ok = modalityPath(call.RoutedPath, "")
		case http.MethodPost:
			ok = modalityPath(call.RoutedPath, "/store-straight") || modalityPath(call.RoutedPath, "/echo")
		}
		if !ok {
			t.Fatalf("Orthanc routes %s %s (request-target %s)", call.Method, call.RoutedPath, call.RawTarget)
		}
	}
}

func TestPathParamEchoUsesTheCapturedModalityID(t *testing.T) {
	base, calls := startOrthancListener(t)
	router := chi.NewRouter()
	router.Post("/modality/{modalityID}/echo", commandController(base).TriggerDICOMEchoSCU)
	router.Delete("/modality/{modalityID}/remove", func(w http.ResponseWriter, r *http.Request) {
		_ = Init(base).DeleteDICOMModality(r.Context(), chi.URLParam(r, "modalityID"))
	})
	cases := []struct {
		method string
		target string
	}{
		{http.MethodPost, "/modality/..%2F..%2Ftools%2Fshutdown/echo"},
		{http.MethodPost, "/modality/x%2F..%2F..%2Ftools%2Fshutdown%3F/echo"},
		{http.MethodDelete, "/modality/..%2F..%2Ftools%2Fshutdown/remove"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			calls.reset()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, nil))
			got := calls.snapshot()
			if len(got) == 0 {
				return
			}
			for _, call := range got {
				suffix := ""
				if call.Method == http.MethodPost {
					suffix = "/echo"
				}
				if !modalityPath(call.RoutedPath, suffix) {
					t.Fatalf("Orthanc routes %s %s (request-target %s)", call.Method, call.RoutedPath, call.RawTarget)
				}
			}
		})
	}
}
