// Package logredact keeps patient identifiers out of the gateway logs.
//
// DICOMweb and worklist URLs carry StudyInstanceUIDs, SeriesInstanceUIDs,
// SOPInstanceUIDs and, for prior-study searches, the PatientID (00100020).
// The access log, the proxy error logs and the application logs all go to
// docker logs, which are kept on disk. This package rewrites them:
//
//   - DICOM UIDs (in paths, query values or free text) become "uid:<8 hex>",
//     a keyed hash, so lines about the same study can still be matched up.
//   - Patient demographics (name, ID, birth date, accession number) and free
//     text search values become "[redacted]".
//
// The hash is an HMAC-SHA256 keyed by LOG_REDACTION_KEY. Without that variable
// a random key is drawn at start-up, so hashes only match within one process.
// The key stops anyone from hashing a list of known UIDs to find a study.
package logredact

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Redacted replaces patient demographics and free text search values.
const Redacted = "[redacted]"

// KeyEnv names the environment variable that holds the hash key.
const KeyEnv = "LOG_REDACTION_KEY"

var (
	keyOnce sync.Once
	key     []byte
)

func hashKey() []byte {
	keyOnce.Do(func() {
		if value := strings.TrimSpace(os.Getenv(KeyEnv)); value != "" {
			key = []byte(value)
			return
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("logredact: cannot draw a random key: " + err.Error())
		}
	})
	return key
}

// SetKeyForTest fixes the hash key. Tests only.
func SetKeyForTest(value string) {
	keyOnce.Do(func() {})
	key = []byte(value)
}

// Hash returns the short keyed hash of a value (8 hex characters).
func Hash(value string) string {
	mac := hmac.New(sha256.New, hashKey())
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))[:8]
}

// UID returns the log form of a DICOM UID: "uid:<8 hex>".
func UID(value string) string {
	return "uid:" + Hash(strings.TrimSpace(value))
}

// uidCandidate finds digit-dot runs; IsUID decides whether one is a UID.
var uidCandidate = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+){2,}\b`)

// IsUID reports whether value looks like a DICOM UID. Version numbers (1.0.5)
// and IPv4 addresses (10.0.0.1) do not: a UID has five or more components,
// or is long (2.25.<128-bit integer> has only three).
func IsUID(value string) bool {
	if len(value) > 64 || !uidCandidate.MatchString(value) || uidCandidate.FindString(value) != value {
		return false
	}
	return strings.Count(value, ".") >= 4 || len(value) >= 20
}

// Parameter classes, keyed by lower-case name.
const (
	classNone = iota
	classUID
	classPHI
)

var paramClass = map[string]int{
	// UIDs: hashed, so requests about the same study can be matched up.
	"studyinstanceuid":    classUID,
	"study_instance_uid":  classUID,
	"studyinstanceuids":   classUID,
	"0020000d":            classUID,
	"seriesinstanceuid":   classUID,
	"series_instance_uid": classUID,
	"0020000e":            classUID,
	"sopinstanceuid":      classUID,
	"sop_instance_uid":    classUID,
	"00080018":            classUID,
	// Patient demographics and accession numbers: never kept.
	"patientname":            classPHI,
	"patient_name":           classPHI,
	"00100010":               classPHI,
	"patientid":              classPHI,
	"patient_id":             classPHI,
	"00100020":               classPHI,
	"issuerofpatientid":      classPHI,
	"00100021":               classPHI,
	"patientbirthdate":       classPHI,
	"patient_birth_date":     classPHI,
	"00100030":               classPHI,
	"otherpatientids":        classPHI,
	"00101000":               classPHI,
	"otherpatientnames":      classPHI,
	"00101001":               classPHI,
	"accessionnumber":        classPHI,
	"accession_number":       classPHI,
	"00080050":               classPHI,
	"referringphysicianname": classPHI,
	"00080090":               classPHI,
	"studyid":                classPHI,
	"00200010":               classPHI,
	// Free text searches may hold a name or an MRN.
	"q":      classPHI,
	"query":  classPHI,
	"search": classPHI,
}

func classOf(name string) int {
	return paramClass[strings.ToLower(strings.TrimSpace(name))]
}

// value rewrites one parameter value according to its class. Values of other
// parameters keep their text unless they are UIDs.
func value(class int, raw string) string {
	if raw == "" {
		return raw
	}
	switch class {
	case classPHI:
		return Redacted
	case classUID:
		decoded, err := url.QueryUnescape(raw)
		if err != nil {
			decoded = raw
		}
		parts := strings.Split(decoded, ",")
		for i, part := range parts {
			parts[i] = UID(part)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return raw
	}
	if hashed, ok := uidList(decoded); ok {
		return hashed
	}
	return raw
}

// uidList hashes a value made of one or more comma-separated UIDs.
func uidList(decoded string) (string, bool) {
	parts := strings.Split(decoded, ",")
	for i, part := range parts {
		if !IsUID(part) {
			return "", false
		}
		parts[i] = UID(part)
	}
	return strings.Join(parts, ","), true
}

// Query redacts a raw query string, keeping the order and the other values.
func Query(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	pairs := strings.Split(rawQuery, "&")
	for i, pair := range pairs {
		name, val, hasValue := strings.Cut(pair, "=")
		if !hasValue {
			continue
		}
		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			decodedName = name
		}
		pairs[i] = name + "=" + value(classOf(decodedName), val)
	}
	return strings.Join(pairs, "&")
}

// Path replaces each path segment that is a UID (or a comma-separated list of
// UIDs) with its hash.
func Path(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if segment == "" {
			continue
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			decoded = segment
		}
		if hashed, ok := uidList(decoded); ok {
			segments[i] = hashed
			continue
		}
		// After /studies/, /series/, /instances/ (and /by-study/) any dotted
		// number is a UID, however short (2.25.4242 is valid).
		if i > 0 && uidRouteParent[strings.ToLower(segments[i-1])] && dottedNumber.MatchString(decoded) {
			segments[i] = UID(decoded)
			continue
		}
		// Anything else that still holds a UID (e.g. "x-<uid>") goes through
		// the text rules.
		segments[i] = uidCandidate.ReplaceAllStringFunc(segment, replaceUID)
	}
	return strings.Join(segments, "/")
}

// uidRouteParent lists the path segments followed by a UID.
var uidRouteParent = map[string]bool{"studies": true, "series": true, "instances": true, "by-study": true}

var dottedNumber = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)

// RequestURI redacts a request URI ("/path?query") for the access log.
func RequestURI(requestURI string) string {
	path, rawQuery, hasQuery := strings.Cut(requestURI, "?")
	if !hasQuery {
		return Path(path)
	}
	return Path(path) + "?" + Query(rawQuery)
}

// URL redacts a full URL for a log line.
func URL(u *url.URL) string {
	if u == nil {
		return ""
	}
	out := u.Scheme + "://" + u.Host + Path(u.EscapedPath())
	if u.RawQuery != "" {
		out += "?" + Query(u.RawQuery)
	}
	return out
}

func replaceUID(match string) string {
	if IsUID(match) {
		return UID(match)
	}
	return match
}

// Text rules for free-form log lines.
var (
	// name=value, name: value and "name":"value"; the name decides (see
	// paramClass and textClass), so free text and URLs inside error messages
	// follow the same rules as query strings.
	textParam = regexp.MustCompile(`\b([A-Za-z0-9_]+)(=|: ?)`)
	// A sensitive value runs to the next separator; "/", "?" and "=" belong
	// to it (an ID like SITE/123 must go whole).
	textValue = regexp.MustCompile(`^[^\s&"',;)\]}]+`)
	jsonParam = regexp.MustCompile(`"([A-Za-z0-9_]+)"(\s*:\s*)"([^"]*)"`)
)

func textClass(name string) int {
	if class := classOf(name); class != classNone {
		return class
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "patient"), strings.HasPrefix(lower, "accession"):
		return classPHI
	case strings.HasSuffix(lower, "uid"), strings.HasSuffix(lower, "uids"):
		return classUID
	}
	return classNone
}

// Text redacts a free-form log line.
func Text(line string) string {
	if line == "" {
		return line
	}
	line = jsonParam.ReplaceAllStringFunc(line, func(match string) string {
		parts := jsonParam.FindStringSubmatch(match)
		switch textClass(parts[1]) {
		case classPHI:
			if parts[3] == "" {
				return match
			}
			return `"` + parts[1] + `"` + parts[2] + `"` + Redacted + `"`
		}
		return match // UIDs fall through to the UID rule below
	})
	line = redactTextParams(line)
	return uidCandidate.ReplaceAllStringFunc(line, replaceUID)
}

// redactTextParams rewrites the values of sensitive name=value pairs. Only a
// sensitive name consumes its value, so a URL after "url=" or "Get " is still
// scanned for the parameters inside it.
func redactTextParams(line string) string {
	var out strings.Builder
	cursor := 0
	for cursor < len(line) {
		loc := textParam.FindStringSubmatchIndex(line[cursor:])
		if loc == nil {
			break
		}
		nameStart, nameEnd, sepEnd := cursor+loc[2], cursor+loc[3], cursor+loc[1]
		class := textClass(line[nameStart:nameEnd])
		if strings.EqualFold(line[nameStart:nameEnd], "uid") && line[nameEnd] == ':' {
			class = classNone // our own "uid:<hash>"; the hash may be all digits
		}
		if class == classNone {
			// Do not scan the value: on "x=a=a=a..." that would be quadratic.
			out.WriteString(line[cursor:sepEnd])
			cursor = sepEnd
			continue
		}
		raw := textValue.FindString(line[sepEnd:])
		if raw == "" {
			out.WriteString(line[cursor:sepEnd])
			cursor = sepEnd
			continue
		}
		out.WriteString(line[cursor:sepEnd])
		out.WriteString(textParamValue(class, raw))
		cursor = sepEnd + len(raw)
	}
	out.WriteString(line[cursor:])
	return out.String()
}

func textParamValue(class int, raw string) string {
	if class == classPHI {
		if strings.HasPrefix(raw, "[") {
			return raw
		}
		return Redacted
	}
	// UIDs: only dotted numbers (after URL decoding), which also leaves
	// "uid:<hash>" alone.
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return raw
	}
	for _, item := range strings.Split(decoded, ",") {
		if !dottedNumber.MatchString(strings.TrimSpace(item)) {
			return raw
		}
	}
	return value(classUID, raw)
}

// Writer redacts every write with Text. The standard log package writes one
// whole line per call, so lines are never split across writes.
type Writer struct {
	Out io.Writer
}

// NewWriter wraps out.
func NewWriter(out io.Writer) *Writer {
	return &Writer{Out: out}
}

func (w *Writer) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.Out, Text(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
