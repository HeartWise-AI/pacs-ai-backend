package rest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func loadWorklistOpenAPI(t *testing.T) map[string]interface{} {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../docs/openapi.json"))
	payload, err := os.ReadFile(path)
	require.NoError(t, err)

	var document map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &document))
	return document
}

func openAPIMap(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	result, ok := value.(map[string]interface{})
	require.True(t, ok)
	return result
}

func TestWorklistOpenAPIDocumentsExactRoutesAndVisibleStudyFilter(t *testing.T) {
	document := loadWorklistOpenAPI(t)
	paths := openAPIMap(t, document["paths"])

	statusPath := openAPIMap(t, paths["/inference/worklist/status"])
	statusOperation := openAPIMap(t, statusPath["get"])
	parameters := statusOperation["parameters"].([]interface{})
	visibleStudyFilter := openAPIMap(t, parameters[0])
	require.Equal(t, "studyInstanceUID", visibleStudyFilter["name"])
	require.Equal(t, "query", visibleStudyFilter["in"])
	require.Equal(t, true, visibleStudyFilter["explode"])
	require.Equal(t, "array", openAPIMap(t, visibleStudyFilter["schema"])["type"])

	eventsPath := openAPIMap(t, paths["/inference/worklist/events"])
	eventsOperation := openAPIMap(t, eventsPath["get"])
	require.Contains(t, eventsOperation["description"], "Reload GET /inference/worklist/status after every reconnect")
	eventsResponses := openAPIMap(t, eventsOperation["responses"])
	eventsOK := openAPIMap(t, eventsResponses["200"])
	eventsContent := openAPIMap(t, eventsOK["content"])
	require.Contains(t, eventsContent, "text/event-stream")

	require.Contains(t, paths, "/inference/worklist/studies/{studyInstanceUID}/runs")
	require.Contains(t, paths, "/inference/processing/runs/{runId}")
	require.Contains(t, paths, "/inference/processing/runs/{runId}/executions/{executionId}/result")
}

func TestWorklistOpenAPIPublicSchemasExcludeTenantAndPythonInternals(t *testing.T) {
	document := loadWorklistOpenAPI(t)
	components := openAPIMap(t, document["components"])
	schemas := openAPIMap(t, components["schemas"])
	publicSchemas := []string{
		"WorklistStudyStatus",
		"WorklistStudyStatusEvent",
		"ProcessingRunExecutionSummary",
		"ProcessingRunSummary",
		"ProcessingRunDetail",
		"ProcessingRunExecutionResult",
	}

	for _, schemaName := range publicSchemas {
		payload, err := json.Marshal(schemas[schemaName])
		require.NoError(t, err)
		for _, forbidden := range []string{"tenantId", "tenant_id", "studyServiceJobId", "study_service_job_id", "resultJson", "result_json", "patientId", "patient_id"} {
			require.NotContains(t, string(payload), forbidden, "%s must not expose %s", schemaName, forbidden)
		}
	}
}

func TestExecutionResultOpenAPIDocumentsLazyNoStoreContractAndSafeErrors(t *testing.T) {
	document := loadWorklistOpenAPI(t)
	paths := openAPIMap(t, document["paths"])
	path := openAPIMap(t, paths["/inference/processing/runs/{runId}/executions/{executionId}/result"])
	operation := openAPIMap(t, path["get"])
	require.Contains(t, operation["description"], "Lazily")
	require.Contains(t, operation["description"], "authenticated owner or administrator")

	responses := openAPIMap(t, operation["responses"])
	for _, status := range []string{"200", "400", "401", "403", "404", "409", "422", "500", "503"} {
		require.Contains(t, responses, status)
	}

	okResponse := openAPIMap(t, responses["200"])
	headers := openAPIMap(t, okResponse["headers"])
	cacheControl := openAPIMap(t, headers["Cache-Control"])
	cacheControlSchema := openAPIMap(t, cacheControl["schema"])
	require.Equal(t, []interface{}{"no-store"}, cacheControlSchema["enum"])

	components := openAPIMap(t, document["components"])
	schemas := openAPIMap(t, components["schemas"])
	resultSchema := openAPIMap(t, schemas["ProcessingRunExecutionResult"])
	resultProperties := openAPIMap(t, resultSchema["properties"])
	opaqueResult := openAPIMap(t, resultProperties["result"])
	require.Equal(t, false, opaqueResult["nullable"])
}
