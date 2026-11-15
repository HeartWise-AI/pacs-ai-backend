package rest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestViewerResultsOpenAPIDocumentsRoutesAndAuthorization(t *testing.T) {
	document := loadWorklistOpenAPI(t)
	paths := openAPIMap(t, document["paths"])

	for _, route := range []string{
		"/inference/worklist/studies/{studyInstanceUID}/latest-results",
		"/inference/processing/runs/{runId}/executions/{executionId}/report",
		"/inference/worklist/studies/{studyInstanceUID}/jobs/{studyServiceJobId}/report",
	} {
		operation := openAPIMap(t, openAPIMap(t, paths[route])["get"])
		require.Contains(t, operation["description"], "authenticated tenant user", route)
	}

	run := openAPIMap(t, openAPIMap(t, paths["/inference/worklist/studies/{studyInstanceUID}/models/{modelId}/run"])["post"])
	require.Contains(t, run["description"], "any authenticated tenant user")
	responses := openAPIMap(t, run["responses"])
	for _, status := range []string{"201", "404", "409", "429", "503"} {
		require.Contains(t, responses, status)
	}

	schemas := openAPIMap(t, openAPIMap(t, document["components"])["schemas"])
	resultProperties := openAPIMap(t, openAPIMap(t, schemas["ProcessingRunExecutionResult"])["properties"])
	require.Contains(t, resultProperties, "selectedViews")
}
