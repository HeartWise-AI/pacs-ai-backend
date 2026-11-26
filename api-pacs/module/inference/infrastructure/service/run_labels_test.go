package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type fakeRunLabelProvider struct {
	jobs    map[string]serviceTypes.StudyServiceJobLabels
	err     error
	added   []string
	removed []string
	users   []string
}

func (provider *fakeRunLabelProvider) GetJobLabels(_ context.Context, tenantID, jobID string) (serviceTypes.StudyServiceJobLabels, bool, error) {
	if provider.err != nil {
		return serviceTypes.StudyServiceJobLabels{}, false, provider.err
	}
	labels, ok := provider.jobs[tenantID+"/"+jobID]
	return labels, ok, nil
}

func (provider *fakeRunLabelProvider) AddJobLabel(_ context.Context, tenantID, jobID, label, createdBy string) (serviceTypes.StudyServiceJobLabels, bool, error) {
	provider.added = append(provider.added, label)
	provider.users = append(provider.users, createdBy)
	labels := provider.jobs[tenantID+"/"+jobID]
	labels.Labels = append(labels.Labels, serviceTypes.RunLabel{Label: label, Key: strings.ToLower(label), Source: "user"})
	return labels, true, nil
}

func (provider *fakeRunLabelProvider) RemoveJobLabel(_ context.Context, tenantID, jobID, label string) (serviceTypes.StudyServiceJobLabels, error) {
	provider.removed = append(provider.removed, label)
	if label == "absent" {
		return serviceTypes.StudyServiceJobLabels{}, errStudyServiceRunLabelNotFound
	}
	return provider.jobs[tenantID+"/"+jobID], nil
}

func (provider *fakeRunLabelProvider) GetRunLabelCatalog(context.Context, string) (serviceTypes.RunLabelCatalog, error) {
	return serviceTypes.RunLabelCatalog{Labels: []serviceTypes.RunLabelCatalogEntry{{Label: "DIAGNOSTIC", Key: "diagnostic", RunCount: 3}}}, provider.err
}

func runLabelService() (*InferenceQueryService, *fakeRunLabelProvider) {
	service, _, _ := viewerResultsService()
	provider := &fakeRunLabelProvider{jobs: map[string]serviceTypes.StudyServiceJobLabels{
		"tenant-a/job-1": {
			JobID: "job-1", StudyInstanceUID: "1.2.3", ModelName: "DeepCORO-JEPA", Modality: "angiogram",
			Labels: []serviceTypes.RunLabel{{Label: "DIAGNOSTIC", Key: "diagnostic", Source: "automatic"}},
		},
	}}
	service.RunLabelProvider = provider
	return service, provider
}

func TestNormalizeRunLabelKeepsFreeTextWithinLimits(t *testing.T) {
	label, err := NormalizeRunLabel("  post \t PCI ")
	require.NoError(t, err)
	require.Equal(t, "post PCI", label)
	label, err = NormalizeRunLabel(strings.Repeat("é", 40))
	require.NoError(t, err, "the limit counts characters, not bytes")
	require.Equal(t, 40, len([]rune(label)))
	for _, bad := range []string{"", "   ", strings.Repeat("x", 41), "a\x00b"} {
		_, err := NormalizeRunLabel(bad)
		require.Error(t, err, "%q", bad)
	}
}

func TestStudyRunLabelsAreScopedToTheStudyAndTheCallersModels(t *testing.T) {
	service, provider := runLabelService()
	target := serviceTypes.GetRunLabels{TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1"}

	labels, err := service.GetStudyRunLabels(context.Background(), target)
	require.NoError(t, err)
	require.Equal(t, "job-1", labels.StudyServiceJobID)
	require.Equal(t, "DIAGNOSTIC", labels.Labels[0].Label)

	other := target
	other.StudyInstanceUID = "9.9.9"
	_, err = service.GetStudyRunLabels(context.Background(), other)
	require.EqualError(t, err, apiError.MissingRecord, "a job of another study is not found")

	foreign := target
	foreign.TenantID = "tenant-b"
	_, err = service.GetStudyRunLabels(context.Background(), foreign)
	require.EqualError(t, err, apiError.MissingRecord)

	denied := target
	denied.AllowModel = func(string) bool { return false }
	_, err = service.GetStudyRunLabels(context.Background(), denied)
	require.EqualError(t, err, apiError.ForbiddenAccess)
	_, _, err = service.AddStudyRunLabel(context.Background(), serviceTypes.ChangeRunLabel{GetRunLabels: denied, UserID: "user-a", Label: "LEFT"})
	require.EqualError(t, err, apiError.ForbiddenAccess)
	require.Empty(t, provider.added, "nothing is written for a model the caller may not use")

	provider.err = errors.New("connection refused")
	_, err = service.GetStudyRunLabels(context.Background(), target)
	require.EqualError(t, err, apiError.InferenceResultServiceUnavailable)
}

func TestAddAndRemoveStudyRunLabels(t *testing.T) {
	service, provider := runLabelService()
	target := serviceTypes.GetRunLabels{TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1"}

	labels, added, err := service.AddStudyRunLabel(context.Background(), serviceTypes.ChangeRunLabel{GetRunLabels: target, UserID: "user-a", Label: "  post   PCI "})
	require.NoError(t, err)
	require.True(t, added)
	require.Equal(t, []string{"post PCI"}, provider.added)
	require.Equal(t, []string{"user-a"}, provider.users, "the label records who added it")
	require.Len(t, labels.Labels, 2)

	_, _, err = service.AddStudyRunLabel(context.Background(), serviceTypes.ChangeRunLabel{GetRunLabels: target, UserID: "user-a", Label: strings.Repeat("x", 41)})
	require.EqualError(t, err, apiError.InvalidPayload)

	_, err = service.RemoveStudyRunLabel(context.Background(), serviceTypes.ChangeRunLabel{GetRunLabels: target, UserID: "user-a", Label: "DIAGNOSTIC"})
	require.NoError(t, err)
	_, err = service.RemoveStudyRunLabel(context.Background(), serviceTypes.ChangeRunLabel{GetRunLabels: target, UserID: "user-a", Label: "absent"})
	require.EqualError(t, err, apiError.MissingRecord)

	catalog, err := service.GetRunLabelCatalog(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Equal(t, "DIAGNOSTIC", catalog.Labels[0].Label)
}

func TestGetStudyLatestModelResultsPassesTheLabelAndListsRunLabels(t *testing.T) {
	service, provider, _ := viewerResultsService()
	diagnostic := serviceTypes.StudyServiceLatestResult{JobID: "job-1", Labels: []string{"DIAGNOSTIC"}, Best: true}
	provider.results = serviceTypes.StudyServiceLatestResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyServiceLatestResult{{
		JobID: "job-1", ModelName: "DeepCORO-JEPA", Modality: "angiogram", Status: "completed",
		Labels: []string{"DIAGNOSTIC"}, Runs: []serviceTypes.StudyServiceLatestResult{diagnostic},
	}, {
		JobID: "job-2", ModelName: "CathEF", Modality: "angiogram", Status: "completed",
	}}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Label: " DIAGNOSTIC ",
	})

	require.NoError(t, err)
	require.Equal(t, "DIAGNOSTIC", provider.label)
	require.Equal(t, "DIAGNOSTIC", *response.Label)
	require.Equal(t, []string{"DIAGNOSTIC"}, response.Results[0].Labels)
	require.Equal(t, []string{"DIAGNOSTIC"}, response.Results[0].Runs[0].Labels)
	payload, err := json.Marshal(response.Results[1])
	require.NoError(t, err)
	require.Contains(t, string(payload), `"labels":[]`, "a run without labels lists none, not null")

	_, err = service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Label: strings.Repeat("x", 41),
	})
	require.EqualError(t, err, apiError.InvalidPayload)
}

func TestStudyServiceDispatcherRunLabelRoutes(t *testing.T) {
	type call struct{ method, path, query, tenant, auth, body string }
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, call{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("X-Tenant-ID"), r.Header.Get("Authorization"), string(body)})
		switch {
		case r.URL.Path == "/run-labels":
			_, _ = w.Write([]byte(`{"labels":[{"label":"DIAGNOSTIC","key":"diagnostic","runCount":2,"lastUsedAt":null}]}`))
		case strings.Contains(r.URL.Path, "missing"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && strings.Contains(string(body), "toolong"):
			w.WriteHeader(http.StatusUnprocessableEntity)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"jobId":"job-1","studyInstanceUID":"1.2.3","modelName":"DeepCORO-JEPA","modality":"angiogram","labels":[{"label":"post PCI","key":"post pci","source":"user"}]}`))
		default:
			_, _ = w.Write([]byte(`{"jobId":"job-1","studyInstanceUID":"1.2.3","modelName":"DeepCORO-JEPA","modality":"angiogram","labels":[]}`))
		}
	}))
	defer server.Close()
	dispatcher := &StudyServiceDispatcher{StudyServiceBaseURL: server.URL, StudyServiceOperatorToken: "operator", StudyServiceClient: server.Client()}
	ctx := context.Background()

	labels, found, err := dispatcher.GetJobLabels(ctx, "tenant-a", "job-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "DeepCORO-JEPA", labels.ModelName)
	_, found, err = dispatcher.GetJobLabels(ctx, "tenant-a", "missing")
	require.NoError(t, err)
	require.False(t, found)

	labels, added, err := dispatcher.AddJobLabel(ctx, "tenant-a", "job-1", "post PCI", "user-a")
	require.NoError(t, err)
	require.True(t, added)
	require.Equal(t, "post PCI", labels.Labels[0].Label)
	_, _, err = dispatcher.AddJobLabel(ctx, "tenant-a", "job-1", "toolong", "user-a")
	require.ErrorIs(t, err, errStudyServiceRunLabelInvalid)

	_, err = dispatcher.RemoveJobLabel(ctx, "tenant-a", "job-1", "post PCI")
	require.NoError(t, err)
	catalog, err := dispatcher.GetRunLabelCatalog(ctx, "tenant-a")
	require.NoError(t, err)
	require.Equal(t, 2, catalog.Labels[0].RunCount)
	_, _, err = dispatcher.GetJobLabels(ctx, " ", "job-1")
	require.Error(t, err, "a tenant is required")

	require.Equal(t, call{http.MethodPost, "/jobs/job-1/labels", "", "tenant-a", "Bearer operator", `{"createdBy":"user-a","label":"post PCI"}`}, calls[2])
	require.Equal(t, call{http.MethodDelete, "/jobs/job-1/labels", "label=post+PCI", "tenant-a", "Bearer operator", ""}, calls[4])
}
