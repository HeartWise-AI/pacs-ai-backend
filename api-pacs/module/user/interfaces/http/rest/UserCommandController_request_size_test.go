package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
)

func TestRegisterTenantUserReturns413ForOversizedBody(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/user/register",
		bytes.NewReader(append([]byte(`{"name":"`), append(bytes.Repeat([]byte("x"), maxPublicRegistrationBodyBytes), []byte(`"}`)...)...)),
	)
	recorder := httptest.NewRecorder()
	controller := UserCommandController{}

	controller.RegisterTenantUser(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.RequestBodyTooLarge, response.ErrorCode)
}
