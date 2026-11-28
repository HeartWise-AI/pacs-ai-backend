package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	orthancAPI "api-pacs/infrastructures/providers/api/orthanc"
	"api-pacs/module/orthanc/domain/entity"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
	serviceTypes "api-pacs/module/orthanc/infrastructure/service/types"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const (
	registeredHost = "203.0.113.10"
	attackerIP     = "198.51.100.23"
	otherHost      = "203.0.113.20"
	registeredAET  = "HOSPITAL_PACS"
	otherAET       = "CLINIC_PACS"
	alphaPatient   = "PATIENT-ALPHA"
	betaPatient    = "PATIENT-BETA"
	alphaPatientID = "ALPHA-1"
	betaPatientID  = "BETA-1"
	alphaStudyUID  = "1.2.840.10008.1.1"
	betaStudyUID   = "1.2.840.10008.1.2"
	alphaSeriesUID = "1.2.840.10008.1.1.1"
	betaSeriesUID  = "1.2.840.10008.1.2.1"
	forgedSeries   = "1.2.840.10008.9.9.1"
	forgedSOP      = "1.2.840.10008.9.9.1.1"
)

// These tests drive UpdateDICOMModality, then apply the DIMSE checks Orthanc
// 1.12.5 (orthancteam/orthanc:24.12.0) performs on the calling AE title and the
// TCP peer. They fail while a peer that is not the registered host can list,
// download, move, or modify studies for every tenant.
func TestSpoofedCallingAETitleCannotReadOrModifySharedStudies(t *testing.T) {
	cfg := orthancConfigFromCompose(t)
	store := newSharedArchive()
	modalities := registerProductionModalities(t, cfg)

	attacker := peer{
		tcpIP: attackerIP,
		// The association request can name the registered host. The peer identity
		// Orthanc checks is the socket address, so this claim must not match.
		claimedPresentationAddress: registeredHost,
		callingAET:                 registeredAET,
		calledAET:                  "PACS_AI",
	}
	require.Equal(t, attackerIP, tcpPeerIdentity(attacker.tcpIP, attacker.reverseDNS))
	require.NotEqual(t, attacker.claimedPresentationAddress, tcpPeerIdentity(attacker.tcpIP, attacker.reverseDNS))
	sameAEDifferentCase := attacker
	sameAEDifferentCase.callingAET = strings.ToLower(registeredAET)
	registered := peer{
		tcpIP:      registeredHost,
		callingAET: registeredAET,
		calledAET:  "PACS_AI",
	}
	unknown := peer{
		tcpIP:      attackerIP,
		callingAET: "UNKNOWN_AE",
		calledAET:  "PACS_AI",
	}

	scp := newDicomSCP(cfg, modalities, store)

	findQuery := map[string]string{
		"QueryRetrieveLevel": "STUDY",
		"PatientName":        "*",
		"PatientID":          "",
		"StudyInstanceUID":   "",
	}
	t.Run("wildcard find from another address", func(t *testing.T) {
		answers := scp.cFindStudies(attacker, findQuery)
		require.Empty(t, answers, "calling AE %s from %s listed studies", attacker.callingAET, attacker.tcpIP)
	})
	t.Run("wildcard find with a different-case AE title", func(t *testing.T) {
		answers := scp.cFindStudies(sameAEDifferentCase, findQuery)
		require.Empty(t, answers, "calling AE %s from %s listed studies", sameAEDifferentCase.callingAET, sameAEDifferentCase.tcpIP)
	})

	t.Run("get of another tenant", func(t *testing.T) {
		instances := scp.cGetStudy(attacker, betaStudyUID)
		require.Empty(t, instances)
	})

	t.Run("move of another tenant to a registered modality", func(t *testing.T) {
		moved := scp.cMoveStudy(attacker, betaStudyUID, otherAET)
		require.Empty(t, moved.destinationHost)
		require.Empty(t, moved.instances)
	})

	t.Run("store into an existing study", func(t *testing.T) {
		before := store.series(betaPatientID, betaStudyUID)
		accepted := scp.cStore(attacker, storedInstance{
			patientID: betaPatientID, patientName: betaPatient,
			studyUID: betaStudyUID, seriesUID: forgedSeries, sopUID: forgedSOP,
			payload: []byte("forged-pixels"),
		})
		require.False(t, accepted)
		require.Equal(t, before, store.series(betaPatientID, betaStudyUID))
	})

	t.Run("store from an unknown calling AE", func(t *testing.T) {
		accepted := scp.cStore(unknown, storedInstance{
			patientID: betaPatientID, patientName: betaPatient,
			studyUID: betaStudyUID, seriesUID: "1.2.840.10008.9.9.2", sopUID: "1.2.840.10008.9.9.2.1",
			payload: []byte("unsolicited-pixels"),
		})
		require.False(t, accepted)
		require.NotContains(t, store.series(betaPatientID, betaStudyUID), "1.2.840.10008.9.9.2")
	})

	t.Run("worklist", func(t *testing.T) {
		require.False(t, scp.accepts(attacker, requestFindWorklist))
	})

	t.Run("storage commitment", func(t *testing.T) {
		require.False(t, scp.accepts(attacker, requestNAction))
		require.False(t, scp.accepts(attacker, requestNEventReport))
		require.Empty(t, scp.commitmentJobs)
		require.Empty(t, scp.commitmentReports)
	})

	t.Run("registered host is inbound store and echo only", func(t *testing.T) {
		answers := scp.cFindStudies(registered, map[string]string{
			"QueryRetrieveLevel": "STUDY",
			"PatientName":        "*",
		})
		require.Empty(t, answers)
		require.Empty(t, scp.cGetStudy(registered, betaStudyUID))
		moved := scp.cMoveStudy(registered, betaStudyUID, otherAET)
		require.Empty(t, moved.instances)
		require.False(t, scp.accepts(registered, requestFindWorklist))
		require.False(t, scp.accepts(registered, requestNAction))
		require.False(t, scp.accepts(registered, requestNEventReport))

		accepted := scp.cStore(registered, storedInstance{
			patientID: alphaPatientID, patientName: alphaPatient,
			studyUID: alphaStudyUID, seriesUID: "1.2.840.10008.1.1.2", sopUID: "1.2.840.10008.1.1.2.1",
			payload: []byte("inbound-store"),
		})
		require.True(t, accepted)
		require.Contains(t, store.series(alphaPatientID, alphaStudyUID), "1.2.840.10008.1.1.2")
		require.True(t, scp.accepts(registered, requestEcho))
	})
}

type peer struct {
	tcpIP                      string
	reverseDNS                 string
	claimedPresentationAddress string
	callingAET                 string
	calledAET                  string
}

type storedInstance struct {
	patientID   string
	patientName string
	studyUID    string
	seriesUID   string
	sopUID      string
	payload     []byte
}

type findAnswer struct {
	PatientName      string
	PatientID        string
	StudyInstanceUID string
}

type moveResult struct {
	destinationHost string
	instances       [][]byte
}

type requestKind int

const (
	requestEcho requestKind = iota
	requestFind
	requestFindWorklist
	requestGet
	requestMove
	requestStore
	requestNAction
	requestNEventReport
)

type modalityRecord struct {
	aet               string
	host              string
	allowEcho         bool
	allowFind         bool
	allowFindWorklist bool
	allowGet          bool
	allowMove         bool
	allowStore        bool
	allowNAction      bool
	allowNEventReport bool
}

type dicomSCP struct {
	cfg               map[string]any
	modalities        []modalityRecord
	archive           *sharedArchive
	commitmentJobs    []string
	commitmentReports []string
}

func newDicomSCP(cfg map[string]any, modalities []modalityRecord, archive *sharedArchive) *dicomSCP {
	return &dicomSCP{cfg: cfg, modalities: modalities, archive: archive}
}

func (s *dicomSCP) cFindStudies(caller peer, query map[string]string) []findAnswer {
	if !s.accepts(caller, requestFind) {
		return nil
	}
	// OrthancFindRequestHandler::Handle runs the lookup with no calling-AE or
	// tenant predicate. Empty values are universal; "*" is a wildcard.
	var answers []findAnswer
	seen := map[string]bool{}
	for _, instance := range s.archive.instances {
		tags := map[string]string{
			"PatientName":      instance.patientName,
			"PatientID":        instance.patientID,
			"StudyInstanceUID": instance.studyUID,
		}
		if !queryMatches(query, tags, s.boolParam("CaseSensitivePN", false)) {
			continue
		}
		if seen[instance.studyUID] {
			continue
		}
		seen[instance.studyUID] = true
		answers = append(answers, findAnswer{
			PatientName: instance.patientName, PatientID: instance.patientID, StudyInstanceUID: instance.studyUID,
		})
	}
	return answers
}

func (s *dicomSCP) cGetStudy(caller peer, studyUID string) [][]byte {
	if !s.accepts(caller, requestGet) {
		return nil
	}
	var payloads [][]byte
	for _, instance := range s.archive.instances {
		if instance.studyUID == studyUID {
			payloads = append(payloads, append([]byte(nil), instance.payload...))
		}
	}
	return payloads
}

func (s *dicomSCP) cMoveStudy(caller peer, studyUID, targetAET string) moveResult {
	if !s.accepts(caller, requestMove) {
		return moveResult{}
	}
	target, ok := s.modalityByAET(targetAET)
	if !ok {
		return moveResult{}
	}
	var payloads [][]byte
	for _, instance := range s.archive.instances {
		if instance.studyUID == studyUID {
			payloads = append(payloads, append([]byte(nil), instance.payload...))
		}
	}
	if len(payloads) == 0 {
		return moveResult{}
	}
	return moveResult{destinationHost: target.host, instances: payloads}
}

func (s *dicomSCP) cStore(caller peer, instance storedInstance) bool {
	if !s.accepts(caller, requestStore) {
		return false
	}
	s.archive.add(instance)
	return true
}

func (s *dicomSCP) accepts(caller peer, kind requestKind) bool {
	remoteIP := tcpPeerIdentity(caller.tcpIP, caller.reverseDNS)
	if !s.allowedConnection(remoteIP, caller.callingAET) {
		return false
	}
	if !s.calledAETAccepted(caller.calledAET) {
		return false
	}
	allowed := s.allowedRequest(remoteIP, caller.callingAET, kind)
	if !allowed {
		return false
	}
	switch kind {
	case requestNAction:
		s.commitmentJobs = append(s.commitmentJobs, caller.callingAET)
	case requestNEventReport:
		s.commitmentReports = append(s.commitmentReports, caller.callingAET)
	}
	return true
}

func (s *dicomSCP) allowedConnection(remoteIP, remoteAET string) bool {
	if s.boolParam("DicomAlwaysAllowEcho", true) ||
		s.boolParam("DicomAlwaysAllowFind", false) ||
		s.boolParam("DicomAlwaysAllowFindWorklist", false) ||
		s.boolParam("DicomAlwaysAllowGet", false) ||
		s.boolParam("DicomAlwaysAllowMove", false) ||
		s.boolParam("DicomAlwaysAllowStore", true) {
		return true
	}
	return s.knownAETitle(remoteAET, remoteIP)
}

func (s *dicomSCP) calledAETAccepted(called string) bool {
	if !s.boolParam("DicomCheckCalledAet", false) {
		return true
	}
	configured, _ := s.cfg["DicomAet"].(string)
	return s.sameAET(called, configured)
}

func (s *dicomSCP) allowedRequest(remoteIP, remoteAET string, kind requestKind) bool {
	switch kind {
	case requestEcho:
		if s.boolParam("DicomAlwaysAllowEcho", true) {
			return true
		}
	case requestFind:
		if s.boolParam("DicomAlwaysAllowFind", false) {
			return true
		}
	case requestFindWorklist:
		if s.boolParam("DicomAlwaysAllowFindWorklist", false) {
			return true
		}
	case requestStore:
		if s.boolParam("DicomAlwaysAllowStore", true) {
			return true
		}
	case requestGet:
		if s.boolParam("DicomAlwaysAllowGet", false) {
			return true
		}
	case requestMove:
		if s.boolParam("DicomAlwaysAllowMove", false) {
			return true
		}
	}

	matches := s.modalitiesByAET(remoteAET)
	if len(matches) == 0 {
		return false
	}
	checkIP := s.boolParam("DicomCheckModalityHost", false)
	if len(matches) == 1 {
		if checkIP && remoteIP != matches[0].host {
			return false
		}
		return matches[0].allows(kind)
	}
	allowedForAll := true
	for _, modality := range matches {
		if modality.allows(kind) {
			if checkIP && modality.host == remoteIP {
				return true
			}
			continue
		}
		allowedForAll = false
	}
	return allowedForAll
}

func (s *dicomSCP) knownAETitle(aet, ip string) bool {
	modality, ok := s.modalityByAET(aet)
	if !ok {
		return false
	}
	return !s.boolParam("DicomCheckModalityHost", false) || ip == modality.host
}

func (s *dicomSCP) modalityByAET(aet string) (modalityRecord, bool) {
	matches := s.modalitiesByAET(aet)
	if len(matches) == 0 {
		return modalityRecord{}, false
	}
	return matches[0], true
}

func (s *dicomSCP) modalitiesByAET(aet string) []modalityRecord {
	var matches []modalityRecord
	for _, modality := range s.modalities {
		if s.sameAET(aet, modality.aet) {
			matches = append(matches, modality)
		}
	}
	return matches
}

func (s *dicomSCP) sameAET(a, b string) bool {
	if s.boolParam("StrictAetComparison", false) {
		return a == b
	}
	return strings.EqualFold(a, b)
}

func (s *dicomSCP) boolParam(key string, fallback bool) bool {
	value, ok := s.cfg[key]
	if !ok {
		return fallback
	}
	parsed, ok := value.(bool)
	if !ok {
		return fallback
	}
	return parsed
}

func (m modalityRecord) allows(kind requestKind) bool {
	switch kind {
	case requestEcho:
		return m.allowEcho
	case requestFind:
		return m.allowFind
	case requestFindWorklist:
		return m.allowFindWorklist
	case requestGet:
		return m.allowGet
	case requestMove:
		return m.allowMove
	case requestStore:
		return m.allowStore
	case requestNAction:
		return m.allowNAction
	case requestNEventReport:
		return m.allowNEventReport
	default:
		return false
	}
}

// tcpPeerIdentity is DCMTK 3.6.8 DUL_ReceiveAssociation: the socket address,
// or its reverse-DNS name when a PTR exists. DUL_FULLDOMAINNAME is off, so a
// name is cut at the first dot. An empty PTR keeps the numeric address.
func tcpPeerIdentity(tcpIP, reverseDNS string) string {
	if reverseDNS == "" {
		return tcpIP
	}
	if label, _, cut := strings.Cut(reverseDNS, "."); cut {
		return label
	}
	return reverseDNS
}

func queryMatches(query, tags map[string]string, caseSensitivePN bool) bool {
	for key, value := range query {
		if key == "QueryRetrieveLevel" || key == "SpecificCharacterSet" || value == "" {
			continue
		}
		got := tags[key]
		if strings.Contains(value, "*") || strings.Contains(value, "?") {
			sensitive := key != "PatientName" || caseSensitivePN
			if !wildcardMatch(value, got, sensitive) {
				return false
			}
			continue
		}
		if key == "PatientName" && !caseSensitivePN {
			if !strings.EqualFold(value, got) {
				return false
			}
			continue
		}
		if value != got {
			return false
		}
	}
	return true
}

func wildcardMatch(pattern, value string, caseSensitive bool) bool {
	if !caseSensitive {
		pattern = strings.ToUpper(pattern)
		value = strings.ToUpper(value)
	}
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	matched, err := regexp.MatchString(b.String(), value)
	return err == nil && matched
}

type sharedArchive struct {
	instances []storedInstance
}

func newSharedArchive() *sharedArchive {
	return &sharedArchive{instances: []storedInstance{
		{patientID: alphaPatientID, patientName: alphaPatient, studyUID: alphaStudyUID, seriesUID: alphaSeriesUID, sopUID: alphaStudyUID + ".1", payload: []byte("alpha-pixels")},
		{patientID: betaPatientID, patientName: betaPatient, studyUID: betaStudyUID, seriesUID: betaSeriesUID, sopUID: betaStudyUID + ".1", payload: []byte("beta-pixels")},
	}}
}

func (a *sharedArchive) add(instance storedInstance) {
	a.instances = append(a.instances, instance)
}

func (a *sharedArchive) series(patientID, studyUID string) []string {
	var series []string
	seen := map[string]bool{}
	for _, instance := range a.instances {
		if instance.patientID == patientID && instance.studyUID == studyUID && !seen[instance.seriesUID] {
			seen[instance.seriesUID] = true
			series = append(series, instance.seriesUID)
		}
	}
	return series
}

func registerProductionModalities(t *testing.T, cfg map[string]any) []modalityRecord {
	t.Helper()
	var records []modalityRecord
	records = append(records, registerModality(t, "tenant-a", "HOSPITAL_PACS", registeredAET, registeredHost))
	records = append(records, registerModality(t, "tenant-b", "CLINIC_PACS", otherAET, otherHost))
	require.True(t, configBool(t, cfg, "DicomModalitiesInDatabase", false))
	return records
}

func registerModality(t *testing.T, tenantID, modalityID, aet, host string) modalityRecord {
	t.Helper()
	var put []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/modalities/"+modalityID, r.URL.Path)
		put = append([]byte(nil), body...)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	command := &OrthancCommandService{
		OrthancCommandRepositoryInterface: modalityRepo{tenantID: tenantID, modalityID: modalityID, aet: aet},
		OrthancAPIInterface:               orthancAPI.Init(server.URL),
	}
	err := command.UpdateDICOMModality(context.Background(), serviceTypes.UpdateDICOMModality{
		TenantID: tenantID, ModalityID: modalityID, AET: aet, Host: host, Port: 104,
		CFindEnabled: true, CMoveEnabled: true, CStoreEnabled: true,
	})
	require.NoError(t, err)
	return unserializeModality(t, modalityID, put)
}

type modalityRepo struct {
	tenantID   string
	modalityID string
	aet        string
}

func (r modalityRepo) SelectDICOMModalityByModalityID(_ context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	return entity.DICOMModality{TenantID: tenantID, ModalityID: modalityID, AET: r.aet}, nil
}

func (modalityRepo) DeleteDICOMModality(context.Context, string, string) error { return nil }

func (modalityRepo) UpsertDICOMModality(context.Context, repositoryTypes.UpsertDICOMModality) error {
	return nil
}

func unserializeModality(t *testing.T, symbolicName string, body []byte) modalityRecord {
	t.Helper()
	require.True(t, symbolicNameAllowed(symbolicName), symbolicName)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	record := modalityRecord{
		aet: "ORTHANC", host: "127.0.0.1",
		allowEcho: true, allowFind: true, allowFindWorklist: true, allowGet: true,
		allowMove: true, allowStore: true, allowNAction: true, allowNEventReport: true,
	}
	record.aet = requireString(t, raw, "AET")
	record.host = requireString(t, raw, "Host")
	if value, ok := raw["AllowEcho"]; ok {
		record.allowEcho = requireBool(t, value)
	}
	if value, ok := raw["AllowFind"]; ok {
		record.allowFind = requireBool(t, value)
	}
	if value, ok := raw["AllowFindWorklist"]; ok {
		record.allowFindWorklist = requireBool(t, value)
	}
	if value, ok := raw["AllowGet"]; ok {
		record.allowGet = requireBool(t, value)
	}
	if value, ok := raw["AllowMove"]; ok {
		record.allowMove = requireBool(t, value)
	}
	if value, ok := raw["AllowStore"]; ok {
		record.allowStore = requireBool(t, value)
	}
	if value, ok := raw["AllowNAction"]; ok {
		record.allowNAction = requireBool(t, value)
	}
	if value, ok := raw["AllowEventReport"]; ok {
		record.allowNEventReport = requireBool(t, value)
	}
	if value, ok := raw["AllowStorageCommitment"]; ok {
		allowed := requireBool(t, value)
		record.allowNAction = allowed
		record.allowNEventReport = allowed
	}
	return record
}

func symbolicNameAllowed(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func orthancConfigFromCompose(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "..", "orthanc", "docker-compose.yml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var compose struct {
		Services map[string]struct {
			Environment map[string]any `yaml:"environment"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &compose))
	orthanc, ok := compose.Services["orthanc"]
	require.True(t, ok)
	// orthancteam/orthanc:24.12.0 orthanc-defaults.json, then ORTHANC__ env vars.
	cfg := map[string]any{
		"StorageDirectory":      "/var/lib/orthanc/db",
		"RemoteAccessAllowed":   true,
		"AuthenticationEnabled": true,
		"HttpsCACertificates":   "/etc/ssl/certs/ca-certificates.crt",
	}
	for key, value := range orthanc.Environment {
		if !strings.HasPrefix(key, "ORTHANC__") {
			continue
		}
		setConfig(cfg, envPath(key), envJSON(value))
	}
	return cfg
}

func envPath(key string) []string {
	rest := strings.TrimPrefix(key, "ORTHANC__")
	var path []string
	for _, token := range strings.Split(rest, "__") {
		var name string
		for _, word := range strings.Split(token, "_") {
			if word == "" {
				continue
			}
			name += strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
		}
		path = append(path, name)
	}
	return path
}

func envJSON(value any) any {
	text, ok := value.(string)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return value
		}
		text = string(encoded)
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return text
	}
	return parsed
}

func setConfig(cfg map[string]any, path []string, value any) {
	if len(path) == 1 {
		cfg[path[0]] = value
		return
	}
	next, _ := cfg[path[0]].(map[string]any)
	if next == nil {
		next = map[string]any{}
		cfg[path[0]] = next
	}
	setConfig(next, path[1:], value)
}

func configBool(t *testing.T, cfg map[string]any, key string, fallback bool) bool {
	t.Helper()
	value, ok := cfg[key]
	if !ok {
		return fallback
	}
	parsed, ok := value.(bool)
	require.True(t, ok, "%s is %T", key, value)
	return parsed
}

func requireString(t *testing.T, raw map[string]any, key string) string {
	t.Helper()
	value, ok := raw[key].(string)
	require.True(t, ok, key)
	return value
}

func requireBool(t *testing.T, value any) bool {
	t.Helper()
	parsed, ok := value.(bool)
	require.True(t, ok)
	return parsed
}
