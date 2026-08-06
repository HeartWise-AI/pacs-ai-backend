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
	}

	for _, schemaName := range publicSchemas {
		payload, err := json.Marshal(schemas[schemaName])
		require.NoError(t, err)
		for _, forbidden := range []string{"tenantId", "tenant_id", "studyServiceJobId", "resultJson"} {
			require.NotContains(t, string(payload), forbidden, "%s must not expose %s", schemaName, forbidden)
		}
	}
}
