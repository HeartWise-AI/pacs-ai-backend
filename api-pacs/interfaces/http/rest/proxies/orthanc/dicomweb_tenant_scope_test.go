package orthanc

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	userApplication "api-pacs/module/user/application"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

const tenantSessionToken = "tenant-a-session"

// cachedInstance is one object in the single Orthanc the deployment shares
// across tenants. Nothing in the object records which tenant retrieved it.
type cachedInstance struct {
	patientName string
	patientID   string
	studyUID    string
	seriesUID   string
	sopUID      string
	pixels      string
}

func tenantInstance(suffix, patient string) cachedInstance {
	study := "1.2.826.0.1.3680043.10.543." + suffix
	return cachedInstance{
		patientName: patient,
		patientID:   "PID-" + patient,
		studyUID:    study,
		seriesUID:   study + ".1",
		sopUID:      study + ".1.1",
		pixels:      "SYNTHETIC-PIXELS-" + patient,
	}
}

var (
	tenantAInstance = tenantInstance("1", "PATIENT-ALPHA")
	tenantBInstance = tenantInstance("2", "PATIENT-BETA")
)

type tenantSessionQuery struct {
	iamApplication.IAMQueryServiceInterface
}

func (query *tenantSessionQuery) GetSessionToken(context.Context, string) (iamEntity.TokenSession, error) {
	return iamEntity.TokenSession{}, fmt.Errorf("unknown session")
}

type tenantASessionQuery struct {
	tenantSessionQuery
	tenantID string
	userID   string
	token    string
}

func (query *tenantASessionQuery) GetSessionToken(_ context.Context, token string) (iamEntity.TokenSession, error) {
	if token != query.token {
		return iamEntity.TokenSession{}, fmt.Errorf("unknown session")
	}
	return iamEntity.TokenSession{
		TenantID: query.tenantID,
		UserID:   query.userID,
		Role:     iamEntity.UserRole,
	}, nil
}

func (query *tenantSessionQuery) IsUserSuspended(context.Context, string, string) (bool, error) {
	return false, nil
}

type acceptedPolicyQuery struct {
	userApplication.UserQueryServiceInterface
}

func (query *acceptedPolicyQuery) GetPolicyStatus(context.Context, string, string) (userTypes.PolicyStatus, error) {
	return userTypes.PolicyStatus{AcceptanceRequired: false, EnforcementActive: true}, nil
}

// dicomWebRouter mounts the same guards and proxy as router.go.
func dicomWebRouter(tenantID, userID, token string) http.Handler {
	middleware := &iamMiddleware.IAMMiddleware{
		IAMQueryServiceInterface: &tenantASessionQuery{
			tenantID: tenantID,
			userID:   userID,
			token:    token,
		},
		UserQueryServiceInterface: &acceptedPolicyQuery{},
	}
	proxy := (&OrthancProxy{}).DICOMWebProxy()
	limited := requestbody.LimitWithScope(requestbody.PositiveInt64FromEnvironment(
		"DICOMWEB_MAX_REQUEST_BODY_BYTES",
		requestbody.DefaultDICOMWebMaxBytes,
	), "dicomweb")(proxy)
	readTimeout := time.Duration(requestbody.PositiveInt64FromEnvironment(
		"DICOMWEB_READ_TIMEOUT_SECONDS",
		int64(requestbody.DefaultDICOMWebReadTimeout/time.Second),
	)) * time.Second

	router := chi.NewRouter()
	router.Route("/proxy", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.TokenSessionOrthancProxyAuthGuard)
			r.Use(middleware.PolicyAcceptanceGuard)
			r.Handle("/orthanc/dicom-web/*", requestbody.WithReadDeadline(readTimeout)(limited))
		})
	})
	return router
}

// sharedOrthanc is the DICOMweb surface of one Orthanc store. It follows the
// plugin's QIDO, WADO, and STOW decisions: queries match the whole store, and
// a stored instance is visible to every later reader of that store.
type sharedOrthanc struct {
	mu        sync.Mutex
	instances []cachedInstance
}

func newSharedOrthanc(seed ...cachedInstance) *sharedOrthanc {
	store := &sharedOrthanc{}
	store.instances = append(store.instances, seed...)
	return store
}

func (store *sharedOrthanc) containsSOP(sopUID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, instance := range store.instances {
		if instance.sopUID == sopUID {
			return true
		}
	}
	return false
}

func (store *sharedOrthanc) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/dicom-web")
	path = strings.Trim(path, "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}

	switch request.Method {
	case http.MethodGet:
		store.serveGet(writer, request, parts)
	case http.MethodPost:
		store.serveSTOW(writer, request, parts)
	default:
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (store *sharedOrthanc) serveGet(writer http.ResponseWriter, request *http.Request, parts []string) {
	switch {
	case len(parts) == 1 && parts[0] == "studies":
		store.writeQIDO(writer, store.match(request.URL.Query(), "", "", ""))
	case len(parts) == 1 && parts[0] == "series":
		store.writeQIDO(writer, store.match(request.URL.Query(), "", "", ""))
	case len(parts) == 1 && parts[0] == "instances":
		store.writeQIDO(writer, store.match(request.URL.Query(), "", "", ""))
	case len(parts) == 2 && parts[0] == "studies":
		store.writeWADO(writer, store.match(nil, parts[1], "", ""))
	case len(parts) == 3 && parts[0] == "studies" && parts[2] == "series":
		store.writeQIDO(writer, store.match(request.URL.Query(), parts[1], "", ""))
	case len(parts) == 3 && parts[0] == "studies" && parts[2] == "metadata":
		store.writeMetadata(writer, store.match(nil, parts[1], "", ""))
	case len(parts) == 3 && parts[0] == "studies" && parts[2] == "rendered":
		store.writePixels(writer, store.match(nil, parts[1], "", ""))
	case len(parts) == 4 && parts[0] == "studies" && parts[2] == "series":
		store.writeWADO(writer, store.match(nil, parts[1], parts[3], ""))
	case len(parts) == 5 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "instances":
		store.writeQIDO(writer, store.match(request.URL.Query(), parts[1], parts[3], ""))
	case len(parts) == 5 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "metadata":
		store.writeMetadata(writer, store.match(nil, parts[1], parts[3], ""))
	case len(parts) == 5 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "rendered":
		store.writePixels(writer, store.match(nil, parts[1], parts[3], ""))
	case len(parts) == 6 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "instances":
		store.writeWADO(writer, store.match(nil, parts[1], parts[3], parts[5]))
	case len(parts) == 7 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "instances" && parts[6] == "metadata":
		store.writeMetadata(writer, store.match(nil, parts[1], parts[3], parts[5]))
	case len(parts) == 7 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "instances" && parts[6] == "frames":
		store.writePixels(writer, store.match(nil, parts[1], parts[3], parts[5]))
	case len(parts) == 7 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "instances" && parts[6] == "rendered":
		store.writePixels(writer, store.match(nil, parts[1], parts[3], parts[5]))
	case len(parts) == 8 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "instances" && (parts[6] == "frames" || parts[6] == "bulk"):
		store.writePixels(writer, store.match(nil, parts[1], parts[3], parts[5]))
	case len(parts) == 9 && parts[0] == "studies" && parts[2] == "series" && parts[4] == "instances" && parts[6] == "frames" && parts[8] == "rendered":
		store.writePixels(writer, store.match(nil, parts[1], parts[3], parts[5]))
	default:
		http.NotFound(writer, request)
	}
}

func (store *sharedOrthanc) match(query url.Values, studyUID, seriesUID, sopUID string) []cachedInstance {
	store.mu.Lock()
	defer store.mu.Unlock()
	var matched []cachedInstance
	for _, instance := range store.instances {
		if studyUID != "" && instance.studyUID != studyUID {
			continue
		}
		if seriesUID != "" && instance.seriesUID != seriesUID {
			continue
		}
		if sopUID != "" && instance.sopUID != sopUID {
			continue
		}
		if query != nil && !queryMatches(instance, query) {
			continue
		}
		matched = append(matched, instance)
	}
	return matched
}

func queryMatches(instance cachedInstance, query url.Values) bool {
	for key, values := range query {
		if len(values) == 0 {
			continue
		}
		value := values[0]
		switch canonicalQIDOKey(key) {
		case "PatientName":
			if !wildcardMatch(value, instance.patientName) {
				return false
			}
		case "PatientID":
			if !wildcardMatch(value, instance.patientID) {
				return false
			}
		case "StudyInstanceUID":
			if value != instance.studyUID {
				return false
			}
		case "SeriesInstanceUID":
			if value != instance.seriesUID {
				return false
			}
		case "SOPInstanceUID":
			if value != instance.sopUID {
				return false
			}
		}
	}
	return true
}

func canonicalQIDOKey(key string) string {
	switch strings.ToLower(strings.ReplaceAll(key, ",", "")) {
	case "patientname", "00100010":
		return "PatientName"
	case "patientid", "00100020":
		return "PatientID"
	case "studyinstanceuid", "0020000d":
		return "StudyInstanceUID"
	case "seriesinstanceuid", "0020000e":
		return "SeriesInstanceUID"
	case "sopinstanceuid", "00080018":
		return "SOPInstanceUID"
	default:
		return ""
	}
}

// wildcardMatch applies Orthanc's DICOM wildcard conversion: '*' becomes '.*',
// '?' becomes '.', and the result must match the whole value.
func wildcardMatch(pattern, value string) bool {
	if !strings.ContainsAny(pattern, "*?") {
		return pattern == value
	}
	expression := "^" + wildcardToRegularExpression(pattern) + "$"
	matched, err := regexp.MatchString(expression, value)
	return err == nil && matched
}

func wildcardToRegularExpression(source string) string {
	replacements := []struct{ old, new string }{
		{`\`, `\\`},
		{"^", `\^`},
		{".", `\.`},
		{"$", `\$`},
		{"|", `\|`},
		{"(", `\(`},
		{")", `\)`},
		{"[", `\[`},
		{"]", `\]`},
		{"+", `\+`},
		{"/", `\/`},
		{"{", `\{`},
		{"}", `\}`},
		{"?", "."},
		{"*", ".*"},
	}
	result := source
	for _, replacement := range replacements {
		result = strings.ReplaceAll(result, replacement.old, replacement.new)
	}
	return result
}

func (store *sharedOrthanc) writeQIDO(writer http.ResponseWriter, instances []cachedInstance) {
	var body bytes.Buffer
	body.WriteByte('[')
	for index, instance := range instances {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"00100010":{"vr":"PN","Value":[{"Alphabetic":"%s"}]},"00100020":{"vr":"LO","Value":["%s"]},"0020000D":{"vr":"UI","Value":["%s"]},"0020000E":{"vr":"UI","Value":["%s"]},"00080018":{"vr":"UI","Value":["%s"]}}`,
			instance.patientName, instance.patientID, instance.studyUID, instance.seriesUID, instance.sopUID)
	}
	body.WriteByte(']')
	writer.Header().Set("Content-Type", "application/dicom+json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body.Bytes())
}

func (store *sharedOrthanc) writeMetadata(writer http.ResponseWriter, instances []cachedInstance) {
	if len(instances) == 0 {
		http.Error(writer, "missing", http.StatusNotFound)
		return
	}
	store.writeQIDO(writer, instances)
}

func (store *sharedOrthanc) writePixels(writer http.ResponseWriter, instances []cachedInstance) {
	if len(instances) == 0 {
		http.Error(writer, "missing", http.StatusNotFound)
		return
	}
	var body bytes.Buffer
	for _, instance := range instances {
		body.WriteString(instance.pixels)
		body.WriteString(" ")
		body.WriteString(instance.patientName)
		body.WriteString(" ")
		body.WriteString(instance.studyUID)
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body.Bytes())
}

func (store *sharedOrthanc) writeWADO(writer http.ResponseWriter, instances []cachedInstance) {
	if len(instances) == 0 {
		http.Error(writer, "missing", http.StatusNotFound)
		return
	}
	var body bytes.Buffer
	body.WriteString("multipart/related ")
	for _, instance := range instances {
		body.WriteString(instance.pixels)
		body.WriteByte(' ')
		body.WriteString(instance.patientName)
		body.WriteByte(' ')
		body.WriteString(instance.patientID)
		body.WriteByte(' ')
		body.WriteString(instance.studyUID)
		body.WriteByte(' ')
		body.WriteString(instance.sopUID)
	}
	writer.Header().Set("Content-Type", `multipart/related; type="application/dicom"`)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body.Bytes())
}

func (store *sharedOrthanc) serveSTOW(writer http.ResponseWriter, request *http.Request, parts []string) {
	expectedStudy := ""
	if len(parts) == 2 && parts[0] == "studies" {
		expectedStudy = parts[1]
	} else if len(parts) != 1 || parts[0] != "studies" {
		http.NotFound(writer, request)
		return
	}

	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/related" || params["type"] != "application/dicom" || params["boundary"] == "" {
		http.Error(writer, "STOW-RS expects multipart/related application/dicom", http.StatusUnsupportedMediaType)
		return
	}
	reader := multipart.NewReader(request.Body, params["boundary"])
	var stored []cachedInstance
	conflict := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(writer, "malformed multipart", http.StatusBadRequest)
			return
		}
		if part.Header.Get("Content-Type") != "application/dicom" {
			http.Error(writer, "part is not application/dicom", http.StatusUnsupportedMediaType)
			return
		}
		payload, err := io.ReadAll(part)
		if err != nil {
			http.Error(writer, "unreadable part", http.StatusBadRequest)
			return
		}
		tags := parseExplicitLE(payload)
		studyUID := tags["0020000D"]
		seriesUID := tags["0020000E"]
		sopUID := tags["00080018"]
		sopClass := tags["00080016"]
		if studyUID == "" || seriesUID == "" || sopUID == "" || sopClass == "" {
			http.Error(writer, "missing dicom tag", http.StatusBadRequest)
			return
		}
		if expectedStudy != "" && studyUID != expectedStudy {
			conflict = true
			continue
		}
		instance := cachedInstance{
			patientName: tags["00100010"],
			patientID:   tags["00100020"],
			studyUID:    studyUID,
			seriesUID:   seriesUID,
			sopUID:      sopUID,
			pixels:      "SYNTHETIC-PIXELS-" + tags["00100010"],
		}
		store.mu.Lock()
		store.instances = append(store.instances, instance)
		store.mu.Unlock()
		stored = append(stored, instance)
	}
	if conflict && len(stored) == 0 {
		http.Error(writer, "study mismatch", http.StatusConflict)
		return
	}
	writer.Header().Set("Content-Type", "application/dicom+json")
	writer.WriteHeader(http.StatusOK)
	var body bytes.Buffer
	body.WriteString(`{"00081190":{"vr":"UR","Value":["studies/`)
	if len(stored) > 0 {
		body.WriteString(stored[0].studyUID)
	}
	body.WriteString(`"]},"00081199":{"vr":"SQ","Value":[`)
	for index, instance := range stored {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"00081155":{"vr":"UI","Value":["%s"]},"00081190":{"vr":"UR","Value":["studies/%s/series/%s/instances/%s"]}}`,
			instance.sopUID, instance.studyUID, instance.seriesUID, instance.sopUID)
	}
	body.WriteString(`]}}`)
	_, _ = writer.Write(body.Bytes())
}

func parseExplicitLE(data []byte) map[string]string {
	if len(data) >= 132 && bytes.Equal(data[128:132], []byte("DICM")) {
		data = data[132:]
	}
	tags := map[string]string{}
	for len(data) >= 8 {
		group := binary.LittleEndian.Uint16(data[0:2])
		element := binary.LittleEndian.Uint16(data[2:4])
		vr := string(data[4:6])
		var value []byte
		if longExplicitVR(vr) {
			if len(data) < 12 {
				break
			}
			length := binary.LittleEndian.Uint32(data[8:12])
			if uint32(len(data)) < 12+length {
				break
			}
			value = data[12 : 12+length]
			data = data[12+length:]
		} else {
			length := binary.LittleEndian.Uint16(data[6:8])
			if len(data) < 8+int(length) {
				break
			}
			value = data[8 : 8+length]
			data = data[8+length:]
		}
		tags[fmt.Sprintf("%04X%04X", group, element)] = strings.TrimRight(string(value), " \x00")
	}
	return tags
}

func longExplicitVR(vr string) bool {
	switch vr {
	case "OB", "OW", "OF", "SQ", "UT", "UN", "UC", "UR":
		return true
	default:
		return false
	}
}

func explicitDICOM(instance cachedInstance) []byte {
	var dataset bytes.Buffer
	appendElement(&dataset, 0x0008, 0x0016, "UI", "1.2.840.10008.5.1.4.1.1.7")
	appendElement(&dataset, 0x0008, 0x0018, "UI", instance.sopUID)
	appendElement(&dataset, 0x0010, 0x0010, "PN", instance.patientName)
	appendElement(&dataset, 0x0010, 0x0020, "LO", instance.patientID)
	appendElement(&dataset, 0x0020, 0x000D, "UI", instance.studyUID)
	appendElement(&dataset, 0x0020, 0x000E, "UI", instance.seriesUID)
	var file bytes.Buffer
	file.Write(make([]byte, 128))
	file.WriteString("DICM")
	file.Write(dataset.Bytes())
	return file.Bytes()
}

func appendElement(buf *bytes.Buffer, group, element uint16, vr, value string) {
	if len(value)%2 == 1 {
		if vr == "UI" {
			value += "\x00"
		} else {
			value += " "
		}
	}
	var header [8]byte
	binary.LittleEndian.PutUint16(header[0:2], group)
	binary.LittleEndian.PutUint16(header[2:4], element)
	copy(header[4:6], vr)
	binary.LittleEndian.PutUint16(header[6:8], uint16(len(value)))
	buf.Write(header[:])
	buf.WriteString(value)
}

func stowBody(instance cachedInstance) (string, []byte) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(map[string][]string{
		"Content-Type": {"application/dicom"},
	})
	if err != nil {
		panic(err)
	}
	if _, err := part.Write(explicitDICOM(instance)); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	contentType := fmt.Sprintf(`multipart/related; type="application/dicom"; boundary=%s`, writer.Boundary())
	return contentType, body.Bytes()
}

func TestSignedInUserCannotReadOrStoreAnotherTenantsImaging(t *testing.T) {
	store := newSharedOrthanc(tenantAInstance, tenantBInstance)
	upstream := httptest.NewServer(store)
	defer upstream.Close()
	t.Setenv("ORTHANC_BASE_URL", upstream.URL)

	handler := dicomWebRouter("tenant-a", "user-a", tenantSessionToken)
	otherTenant := dicomWebRouter("tenant-b", "user-b", "tenant-b-session")
	foreign := []string{
		tenantBInstance.patientName,
		tenantBInstance.patientID,
		tenantBInstance.studyUID,
		tenantBInstance.seriesUID,
		tenantBInstance.sopUID,
		tenantBInstance.pixels,
	}

	stowed := tenantInstance("9", "PATIENT-STOWED")
	stowType, stowPayload := stowBody(stowed)
	addedToForeign := tenantBInstance
	addedToForeign.sopUID = tenantBInstance.studyUID + ".1.9"
	addedToForeign.pixels = "SYNTHETIC-PIXELS-ADDED"
	addedType, addedPayload := stowBody(addedToForeign)

	requests := []struct {
		name        string
		method      string
		path        string
		query       string
		body        []byte
		contentType string
		absentSOP   string
	}{
		{name: "qido studies by patient wildcard", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies", query: "PatientName=*"},
		{name: "qido studies by foreign study uid", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies", query: "StudyInstanceUID=" + tenantBInstance.studyUID},
		{name: "qido studies by tag keyword and wildcard", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies", query: "00100010=*"},
		{name: "qido all series", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/series", query: "PatientName=*"},
		{name: "qido all instances", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/instances", query: "PatientName=*"},
		{name: "qido series of foreign study", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series"},
		{name: "qido instances of foreign series", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series/" + tenantBInstance.seriesUID + "/instances"},
		{name: "wado foreign study", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID},
		{name: "wado foreign series", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series/" + tenantBInstance.seriesUID},
		{name: "wado foreign instance", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series/" + tenantBInstance.seriesUID + "/instances/" + tenantBInstance.sopUID},
		{name: "wado foreign metadata", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/metadata"},
		{name: "wado foreign instance metadata", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series/" + tenantBInstance.seriesUID + "/instances/" + tenantBInstance.sopUID + "/metadata"},
		{name: "wado foreign frames", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series/" + tenantBInstance.seriesUID + "/instances/" + tenantBInstance.sopUID + "/frames/1"},
		{name: "wado foreign bulk", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series/" + tenantBInstance.seriesUID + "/instances/" + tenantBInstance.sopUID + "/bulk/7fe00010"},
		{name: "wado foreign rendered", method: http.MethodGet, path: "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/rendered"},
		{
			name:   "wado path is the foreign study even if the query names the caller study",
			method: http.MethodGet,
			path:   "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID + "/series/" + tenantBInstance.seriesUID + "/instances/" + tenantBInstance.sopUID,
			query:  "StudyInstanceUID=" + tenantAInstance.studyUID,
		},
		{
			name:        "stow into the shared store",
			method:      http.MethodPost,
			path:        "/proxy/orthanc/dicom-web/studies",
			body:        stowPayload,
			contentType: stowType,
			absentSOP:   stowed.sopUID,
		},
		{
			name:        "stow into a foreign study",
			method:      http.MethodPost,
			path:        "/proxy/orthanc/dicom-web/studies/" + tenantBInstance.studyUID,
			body:        addedPayload,
			contentType: addedType,
			absentSOP:   addedToForeign.sopUID,
		},
	}

	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.path
			if tc.query != "" {
				target += "?" + tc.query
			}
			var body io.Reader
			if tc.body != nil {
				body = bytes.NewReader(tc.body)
			}
			request := httptest.NewRequest(tc.method, target, body)
			request.Header.Set("Authorization", "Bearer "+tenantSessionToken)
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			response := recorder.Body.String()
			require.Less(t, recorder.Code, 500, "proxy error: %s", response)
			for _, marker := range foreign {
				require.NotContains(t, response, marker)
			}
			if tc.absentSOP == "" {
				return
			}
			// A refusal must not leave the object in the shared store.
			if recorder.Code >= 400 {
				require.False(t, store.containsSOP(tc.absentSOP), "shared Orthanc stored %s before refusing", tc.absentSOP)
			}
			follow := httptest.NewRequest(http.MethodGet, "/proxy/orthanc/dicom-web/studies?StudyInstanceUID="+studyUIDFromSOP(tc.absentSOP), nil)
			follow.Header.Set("Authorization", "Bearer tenant-b-session")
			followed := httptest.NewRecorder()
			otherTenant.ServeHTTP(followed, follow)
			require.Less(t, followed.Code, 500, "proxy error: %s", followed.Body.String())
			require.NotContains(t, followed.Body.String(), tc.absentSOP)
			require.NotContains(t, followed.Body.String(), "PATIENT-STOWED")
			require.NotContains(t, followed.Body.String(), "SYNTHETIC-PIXELS-ADDED")
		})
	}
}

func studyUIDFromSOP(sopUID string) string {
	// SOP is <study>.1.1 or <study>.1.9. The study is the UID without the last two components.
	parts := strings.Split(sopUID, ".")
	requireParts := len(parts) - 2
	if requireParts < 1 {
		return sopUID
	}
	return strings.Join(parts[:requireParts], ".")
}

func TestAnonymousDICOMWebProxyDoesNotReachOrthanc(t *testing.T) {
	store := newSharedOrthanc(tenantAInstance, tenantBInstance)
	upstream := httptest.NewServer(store)
	defer upstream.Close()
	t.Setenv("ORTHANC_BASE_URL", upstream.URL)

	request := httptest.NewRequest(http.MethodGet, "/proxy/orthanc/dicom-web/studies?PatientName=*", nil)
	recorder := httptest.NewRecorder()
	dicomWebRouter("tenant-a", "user-a", tenantSessionToken).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.NotContains(t, recorder.Body.String(), tenantBInstance.patientName)
	require.NotContains(t, recorder.Body.String(), tenantBInstance.studyUID)
}
