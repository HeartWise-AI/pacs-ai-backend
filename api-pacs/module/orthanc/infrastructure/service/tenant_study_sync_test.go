package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type syncStore struct {
	mu       sync.Mutex
	owners   map[string]map[string]bool
	evidence map[string][]string
	inserts  int
}

func newSyncStore() *syncStore {
	return &syncStore{owners: map[string]map[string]bool{}, evidence: map[string][]string{}}
}

func (store *syncStore) InsertTenantStudy(_ context.Context, tenantID, study string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.owners[study] == nil {
		store.owners[study] = map[string]bool{}
	}
	if !store.owners[study][tenantID] {
		store.inserts++
	}
	store.owners[study][tenantID] = true
	return nil
}

func (store *syncStore) SelectTenantStudies(_ context.Context, tenantID string, studies []string) ([]string, error) {
	return nil, nil
}

func (store *syncStore) SelectOwnedStudies(_ context.Context, studies []string) ([]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []string
	for _, study := range studies {
		if len(store.owners[study]) > 0 {
			out = append(out, study)
		}
	}
	return out, nil
}

func (store *syncStore) SeesEveryStudy(context.Context, string) (bool, error) { return false, nil }

func (store *syncStore) SelectTenantStudyEvidence(_ context.Context, studies []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, study := range studies {
		if tenants, ok := store.evidence[study]; ok {
			out[study] = tenants
		}
	}
	return out, nil
}

func (store *syncStore) ownersOf(study string) []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []string
	for tenant := range store.owners[study] {
		out = append(out, tenant)
	}
	return out
}

// fakeOrthanc serves /studies (expand, since, limit), /studies/{id} and
// /changes like Orthanc's REST API.
type fakeOrthanc struct {
	mu      sync.Mutex
	studies []orthancStudy
	changes []map[string]interface{}
}

func (orthanc *fakeOrthanc) add(id, uid string, labels ...string) {
	orthanc.mu.Lock()
	defer orthanc.mu.Unlock()
	orthanc.studies = append(orthanc.studies, orthancStudy{ID: id, Labels: labels, MainDicomTags: map[string]string{"StudyInstanceUID": uid}})
	orthanc.changes = append(orthanc.changes, map[string]interface{}{"ChangeType": "NewStudy", "ID": id, "ResourceType": "Study", "Seq": len(orthanc.changes) + 1})
}

func (orthanc *fakeOrthanc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	orthanc.mu.Lock()
	defer orthanc.mu.Unlock()
	query := r.URL.Query()
	switch {
	case r.URL.Path == "/studies":
		since, _ := strconv.Atoi(query.Get("since"))
		limit, _ := strconv.Atoi(query.Get("limit"))
		page := []orthancStudy{}
		for index := since; index < len(orthanc.studies) && (limit == 0 || index < since+limit); index++ {
			page = append(page, orthanc.studies[index])
		}
		_ = json.NewEncoder(w).Encode(page)
	case strings.HasPrefix(r.URL.Path, "/studies/"):
		id := strings.TrimPrefix(r.URL.Path, "/studies/")
		for _, study := range orthanc.studies {
			if study.ID == id {
				_ = json.NewEncoder(w).Encode(study)
				return
			}
		}
		http.NotFound(w, r)
	case r.URL.Path == "/changes":
		last := len(orthanc.changes)
		if query.Has("last") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"Changes": []interface{}{}, "Done": true, "Last": last})
			return
		}
		since, _ := strconv.Atoi(query.Get("since"))
		var out []map[string]interface{}
		for _, change := range orthanc.changes {
			if change["Seq"].(int) > since {
				out = append(out, change)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"Changes": out, "Done": true, "Last": last})
	default:
		http.NotFound(w, r)
	}
}

func TestBackfillAssignsEveryCachedStudyByTheRule(t *testing.T) {
	orthanc := &fakeOrthanc{}
	orthanc.add("o-retrieved", "1.2.3.1")
	orthanc.add("o-ingested", "1.2.3.2")
	orthanc.add("o-ingested-twice", "1.2.3.3")
	orthanc.add("o-labelled", "1.2.3.4", "pacs-ai-tenant-tenant-c", "unrelated-label")
	orthanc.add("o-pushed", "1.2.3.5")
	server := httptest.NewServer(orthanc)
	defer server.Close()

	newStore := func() *syncStore {
		store := newSyncStore()
		_ = store.InsertTenantStudy(context.Background(), "tenant-b", "1.2.3.1") // retrieved by tenant B
		store.evidence["1.2.3.2"] = []string{"tenant-a"}
		store.evidence["1.2.3.3"] = []string{"tenant-a", "tenant-b", "tenant-a"}
		return store
	}

	t.Run("multi-tenant install: evidence and labels only, the rest stays unassigned", func(t *testing.T) {
		store := newStore()
		sync := &TenantStudySync{Store: store, OrthancBaseURL: server.URL, PageSize: 2}
		report, err := sync.Backfill(context.Background())
		require.NoError(t, err)
		require.Equal(t, TenantStudySyncReport{Studies: 5, AlreadyAssigned: 1, ByEvidence: 2, ByLabel: 1, Unassigned: 1, UnassignedOrthancIDs: []string{"o-pushed"}}, report)
		require.ElementsMatch(t, []string{"tenant-b"}, store.ownersOf("1.2.3.1"))
		require.ElementsMatch(t, []string{"tenant-a"}, store.ownersOf("1.2.3.2"))
		require.ElementsMatch(t, []string{"tenant-a", "tenant-b"}, store.ownersOf("1.2.3.3"))
		require.ElementsMatch(t, []string{"tenant-c"}, store.ownersOf("1.2.3.4"))
		require.Empty(t, store.ownersOf("1.2.3.5"))
	})

	t.Run("single-tenant install: studies without evidence go to the default tenant", func(t *testing.T) {
		store := newStore()
		sync := &TenantStudySync{Store: store, OrthancBaseURL: server.URL, DefaultTenantID: "tenant-a", PageSize: 2}
		report, err := sync.Backfill(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, report.ByDefault)
		require.Zero(t, report.Unassigned)
		require.ElementsMatch(t, []string{"tenant-a"}, store.ownersOf("1.2.3.5"))
		// the retrieved study keeps its owner; nothing is guessed on top
		require.ElementsMatch(t, []string{"tenant-b"}, store.ownersOf("1.2.3.1"))

		inserts := store.inserts
		again, err := sync.Backfill(context.Background())
		require.NoError(t, err)
		require.Equal(t, 5, again.AlreadyAssigned, "a second run finds every study assigned")
		require.Equal(t, inserts, store.inserts, "a second run writes nothing")
	})

	t.Run("dry run writes nothing", func(t *testing.T) {
		store := newStore()
		before := store.inserts
		sync := &TenantStudySync{Store: store, OrthancBaseURL: server.URL, DefaultTenantID: "tenant-a", DryRun: true}
		report, err := sync.Backfill(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, report.ByDefault)
		require.Equal(t, before, store.inserts)
	})
}

func TestFollowAssignsStudiesPushedAfterTheBackfill(t *testing.T) {
	orthanc := &fakeOrthanc{}
	orthanc.add("o-old", "1.2.4.1")
	server := httptest.NewServer(orthanc)
	defer server.Close()

	store := newSyncStore()
	sync := &TenantStudySync{Store: store, OrthancBaseURL: server.URL, DefaultTenantID: "tenant-a"}
	cursor, err := sync.lastChange(context.Background())
	require.NoError(t, err)
	_, err = sync.Backfill(context.Background())
	require.NoError(t, err)

	for index := 0; index < 3; index++ {
		orthanc.add(fmt.Sprintf("o-pushed-%d", index), fmt.Sprintf("1.2.4.%d", 10+index))
	}
	cursor, report, err := sync.Follow(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, 4, cursor)
	require.Equal(t, 3, report.ByDefault)
	for index := 0; index < 3; index++ {
		require.ElementsMatch(t, []string{"tenant-a"}, store.ownersOf(fmt.Sprintf("1.2.4.%d", 10+index)))
	}

	// nothing new: nothing to do
	cursor, report, err = sync.Follow(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, 4, cursor)
	require.Zero(t, report.Studies)
}

func TestFollowKeepsItsCursorWhenOrthancFails(t *testing.T) {
	orthanc := &fakeOrthanc{}
	failing := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing && strings.HasPrefix(r.URL.Path, "/studies/") {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		orthanc.ServeHTTP(w, r)
	}))
	defer server.Close()

	store := newSyncStore()
	sync := &TenantStudySync{Store: store, OrthancBaseURL: server.URL, DefaultTenantID: "tenant-a"}
	orthanc.add("o-new", "1.2.5.1")
	cursor, _, err := sync.Follow(context.Background(), 0)
	require.Error(t, err)
	require.Equal(t, 0, cursor, "the change is read again next time")
	require.Empty(t, store.ownersOf("1.2.5.1"))

	failing = false
	cursor, report, err := sync.Follow(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, 1, cursor)
	require.Equal(t, 1, report.ByDefault)

	// a study deleted since its change (404) is skipped and the cursor moves on
	orthanc.mu.Lock()
	orthanc.changes = append(orthanc.changes, map[string]interface{}{"ChangeType": "NewStudy", "ID": "o-gone", "ResourceType": "Study", "Seq": 2})
	orthanc.mu.Unlock()
	cursor, _, err = sync.Follow(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, 2, cursor)
}
