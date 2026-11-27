package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"api-pacs/module/orthanc/domain/repository"
)

// TenantLabelPrefix marks a study in Orthanc as uploaded for a tenant: an
// uploader that writes to Orthanc directly (the SCAD intake or a smoke test
// with upload_dicoms.py) adds the label "pacs-ai-tenant-<tenantID>" to the
// study. Orthanc labels allow letters, digits, '_' and '-'.
const TenantLabelPrefix = "pacs-ai-tenant-"

// TenantStudySync assigns the studies of the shared Orthanc to tenants.
//
// A study that a tenant already owns is left alone. Otherwise its tenants are,
// in order:
//  1. the tenants the gateway's ingestion records tie to it (candidates and
//     processing runs, written when the tenant's own PACS listed the study);
//  2. the tenants named by a "pacs-ai-tenant-<id>" label on the study;
//  3. the install's default tenant (ORTHANC_DEFAULT_TENANT_ID), for a
//     single-tenant install;
//  4. none: the study stays unassigned, is logged, and no tenant reads it.
//
// Nothing is guessed: with several tenants and no evidence the study waits
// for a retrieve, an ingestion record, a label or an upload through the
// gateway. Retrieves (/orthanc/modality/retrieve) and STOW-RS through the
// gateway record their tenant themselves.
type TenantStudySync struct {
	Store           repository.OrthancTenantStudyOwnershipInterface
	OrthancBaseURL  string
	DefaultTenantID string
	Client          *http.Client
	// DryRun counts what would be assigned without writing.
	DryRun bool
	// PageSize is the number of studies read from Orthanc per request.
	PageSize int
}

// TenantStudySyncReport counts what a sync pass did.
type TenantStudySyncReport struct {
	Studies         int `json:"studies"`
	AlreadyAssigned int `json:"alreadyAssigned"`
	ByEvidence      int `json:"assignedByIngestionRecords"`
	ByLabel         int `json:"assignedByLabel"`
	ByDefault       int `json:"assignedToDefaultTenant"`
	Unassigned      int `json:"unassigned"`
	// UnassignedOrthancIDs lists Orthanc's resource IDs (not DICOM UIDs) of the
	// studies left unassigned, at most 50.
	UnassignedOrthancIDs []string `json:"unassignedOrthancIds,omitempty"`
}

func (report *TenantStudySyncReport) add(other TenantStudySyncReport) {
	report.Studies += other.Studies
	report.AlreadyAssigned += other.AlreadyAssigned
	report.ByEvidence += other.ByEvidence
	report.ByLabel += other.ByLabel
	report.ByDefault += other.ByDefault
	report.Unassigned += other.Unassigned
	for _, id := range other.UnassignedOrthancIDs {
		if len(report.UnassignedOrthancIDs) < 50 {
			report.UnassignedOrthancIDs = append(report.UnassignedOrthancIDs, id)
		}
	}
}

type orthancStudy struct {
	ID            string            `json:"ID"`
	Labels        []string          `json:"Labels"`
	MainDicomTags map[string]string `json:"MainDicomTags"`
}

func (study orthancStudy) uid() string {
	return strings.TrimSpace(study.MainDicomTags["StudyInstanceUID"])
}

func (sync *TenantStudySync) client() *http.Client {
	if sync.Client != nil {
		return sync.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (sync *TenantStudySync) getJSON(ctx context.Context, path string, query url.Values, target interface{}) error {
	endpoint := strings.TrimSuffix(sync.OrthancBaseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := sync.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return orthancStatusError{path: path, status: response.StatusCode}
	}
	return json.NewDecoder(response.Body).Decode(target)
}

// Backfill assigns every study currently in Orthanc. It is idempotent.
func (sync *TenantStudySync) Backfill(ctx context.Context) (TenantStudySyncReport, error) {
	pageSize := sync.PageSize
	if pageSize <= 0 {
		pageSize = 500
	}
	var report TenantStudySyncReport
	for since := 0; ; since += pageSize {
		var studies []orthancStudy
		query := url.Values{"expand": {"true"}, "since": {fmt.Sprint(since)}, "limit": {fmt.Sprint(pageSize)}}
		if err := sync.getJSON(ctx, "/studies", query, &studies); err != nil {
			return report, err
		}
		page, err := sync.assign(ctx, studies)
		report.add(page)
		if err != nil {
			return report, err
		}
		if len(studies) < pageSize {
			return report, nil
		}
	}
}

// assign gives each study without an owner its tenants.
func (sync *TenantStudySync) assign(ctx context.Context, studies []orthancStudy) (TenantStudySyncReport, error) {
	var report TenantStudySyncReport
	var uids []string
	byUID := map[string]orthancStudy{}
	for _, study := range studies {
		uid := study.uid()
		if uid == "" {
			continue
		}
		if _, seen := byUID[uid]; !seen {
			uids = append(uids, uid)
		}
		byUID[uid] = study
	}
	report.Studies = len(uids)
	if len(uids) == 0 {
		return report, nil
	}

	owned, err := sync.Store.SelectOwnedStudies(ctx, uids)
	if err != nil {
		return report, err
	}
	ownedSet := map[string]struct{}{}
	for _, uid := range owned {
		ownedSet[uid] = struct{}{}
	}
	var unowned []string
	for _, uid := range uids {
		if _, ok := ownedSet[uid]; ok {
			report.AlreadyAssigned++
			continue
		}
		unowned = append(unowned, uid)
	}
	if len(unowned) == 0 {
		return report, nil
	}

	evidence, err := sync.Store.SelectTenantStudyEvidence(ctx, unowned)
	if err != nil {
		return report, err
	}
	for _, uid := range unowned {
		tenants, source := sync.tenantsOf(uid, byUID[uid], evidence)
		if len(tenants) == 0 {
			report.Unassigned++
			if len(report.UnassignedOrthancIDs) < 50 {
				report.UnassignedOrthancIDs = append(report.UnassignedOrthancIDs, byUID[uid].ID)
			}
			log.Printf("[orthanc-tenant-studies] study left unassigned: no tenant evidence and no default tenant orthanc_id=%s", byUID[uid].ID)
			continue
		}
		if !sync.DryRun {
			for _, tenantID := range tenants {
				if err := sync.Store.InsertTenantStudy(ctx, tenantID, uid); err != nil {
					return report, err
				}
			}
		}
		switch source {
		case "evidence":
			report.ByEvidence++
		case "label":
			report.ByLabel++
		default:
			report.ByDefault++
		}
	}
	return report, nil
}

func (sync *TenantStudySync) tenantsOf(uid string, study orthancStudy, evidence map[string][]string) ([]string, string) {
	if tenants := uniqueSorted(evidence[uid]); len(tenants) > 0 {
		return tenants, "evidence"
	}
	var labelled []string
	for _, label := range study.Labels {
		if tenantID := strings.TrimPrefix(label, TenantLabelPrefix); tenantID != label && tenantID != "" {
			labelled = append(labelled, tenantID)
		}
	}
	if tenants := uniqueSorted(labelled); len(tenants) > 0 {
		return tenants, "label"
	}
	if sync.DefaultTenantID != "" {
		return []string{sync.DefaultTenantID}, "default"
	}
	return nil, ""
}

func uniqueSorted(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

type orthancStatusError struct {
	path   string
	status int
}

func (err orthancStatusError) Error() string {
	return fmt.Sprintf("orthanc %s answered %d", err.path, err.status)
}

type orthancChanges struct {
	Changes []struct {
		ChangeType   string `json:"ChangeType"`
		ID           string `json:"ID"`
		ResourceType string `json:"ResourceType"`
	} `json:"Changes"`
	Done bool `json:"Done"`
	Last int  `json:"Last"`
}

// lastChange returns the sequence number of Orthanc's latest change.
func (sync *TenantStudySync) lastChange(ctx context.Context) (int, error) {
	var changes orthancChanges
	if err := sync.getJSON(ctx, "/changes", url.Values{"last": {""}}, &changes); err != nil {
		return 0, err
	}
	return changes.Last, nil
}

// Follow assigns the studies that arrive after since: every NewStudy and
// StableStudy change in Orthanc's change log. It returns the new cursor.
func (sync *TenantStudySync) Follow(ctx context.Context, since int) (int, TenantStudySyncReport, error) {
	var report TenantStudySyncReport
	for {
		var changes orthancChanges
		if err := sync.getJSON(ctx, "/changes", url.Values{"since": {fmt.Sprint(since)}, "limit": {"200"}}, &changes); err != nil {
			return since, report, err
		}
		seen := map[string]struct{}{}
		var studies []orthancStudy
		for _, change := range changes.Changes {
			if change.ChangeType != "NewStudy" && change.ChangeType != "StableStudy" {
				continue
			}
			if _, ok := seen[change.ID]; ok {
				continue
			}
			seen[change.ID] = struct{}{}
			var study orthancStudy
			if err := sync.getJSON(ctx, "/studies/"+url.PathEscape(change.ID), nil, &study); err != nil {
				var status orthancStatusError
				if errors.As(err, &status) && status.status == http.StatusNotFound {
					continue // deleted since (cache purge): nothing to assign
				}
				// keep the cursor: this page is read again next time
				return since, report, err
			}
			studies = append(studies, study)
		}
		page, err := sync.assign(ctx, studies)
		report.add(page)
		if err != nil {
			return since, report, err
		}
		if changes.Last > since {
			since = changes.Last
		}
		if changes.Done || len(changes.Changes) == 0 {
			return since, report, nil
		}
	}
}

// Run backfills once, then follows Orthanc's change log every interval. The
// change cursor is taken before the backfill, so a study arriving during the
// backfill is still seen.
func (sync *TenantStudySync) Run(ctx context.Context, interval time.Duration) {
	cursor := -1
	backfilled := false
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if cursor < 0 {
			if last, err := sync.lastChange(ctx); err != nil {
				log.Printf("[orthanc-tenant-studies] cannot read Orthanc's change log: %v", err)
			} else {
				cursor = last
			}
		}
		if cursor >= 0 && !backfilled {
			report, err := sync.Backfill(ctx)
			if err != nil {
				log.Printf("[orthanc-tenant-studies] backfill failed, retrying: %v", err)
			} else {
				backfilled = true
				log.Printf("[orthanc-tenant-studies] backfill studies=%d already=%d ingestion=%d label=%d default=%d unassigned=%d",
					report.Studies, report.AlreadyAssigned, report.ByEvidence, report.ByLabel, report.ByDefault, report.Unassigned)
			}
		}
		if cursor >= 0 && backfilled {
			next, report, err := sync.Follow(ctx, cursor)
			if err != nil {
				log.Printf("[orthanc-tenant-studies] following Orthanc's change log failed: %v", err)
			}
			cursor = next
			if report.Studies > report.AlreadyAssigned || report.Unassigned > 0 {
				log.Printf("[orthanc-tenant-studies] new studies=%d already=%d ingestion=%d label=%d default=%d unassigned=%d",
					report.Studies, report.AlreadyAssigned, report.ByEvidence, report.ByLabel, report.ByDefault, report.Unassigned)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
