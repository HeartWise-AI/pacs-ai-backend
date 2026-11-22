package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryLocalResourceRequestedTagsContract(t *testing.T) {
	request, err := json.Marshal(QueryLocalResourceRequest{
		Level: "Study", Query: QueryLocalResource{StudyInstanceUID: "1.2.3"}, Expand: true,
		RequestedTags: []string{"ModalitiesInStudy", "PatientID"},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"Level":"Study",
		"Query":{"StudyInstanceUID":"1.2.3"},
		"Expand":true,
		"RequestedTags":["ModalitiesInStudy","PatientID"]
	}`, string(request))

	var response GetLocalResourceResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"ID":"orthanc-study-1",
		"RequestedTags":{"ModalitiesInStudy":"XA\\SR","PatientID":"patient-1"}
	}`), &response))
	require.Equal(t, "orthanc-study-1", response.ID)
	require.Equal(t, "XA\\SR", response.RequestedTags["ModalitiesInStudy"])
}
