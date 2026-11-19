package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	iamService "api-pacs/module/iam/infrastructure/service"
)

type fakeHandoffService struct {
	mint        iamService.MintHandoffRequest
	mintResult  iamService.MintHandoffResult
	err         error
	code, ip    string
	tenant, uid string
}

func (f *fakeHandoffService) MintHandoff(_ context.Context, request iamService.MintHandoffRequest) (iamService.MintHandoffResult, error) {
	f.mint = request
	return f.mintResult, f.err
}

func (f *fakeHandoffService) ExchangeHandoff(_ context.Context, code, clientIP string) (iamService.ExchangeHandoffResult, error) {
	f.code, f.ip = code, clientIP
	return iamService.ExchangeHandoffResult{SessionToken: "s-1", TenantID: "t1", ReturnPath: "/viewer"}, f.err
}

func (f *fakeHandoffService) CompleteHandoffLink(_ context.Context, code, tenantID, userID string) (string, error) {
	f.code, f.tenant, f.uid = code, tenantID, userID
	return "/viewer", f.err
}

func handoffBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	data, _ := body["data"].(map[string]interface{})
	return data
}

const mintJSON = `{"tenantId":"t1","externalUid":"fb-1","email":"a@b.c","emailVerified":true,"returnPath":"/viewer"}`

func mintRequestHTTP(token string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/internal/auth/handoff", strings.NewReader(mintJSON))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func TestMintNeedsTheServiceTokenAndTheInternalNetwork(t *testing.T) {
	service := &fakeHandoffService{mintResult: iamService.MintHandoffResult{URL: "https://p/auth/handoff#code=x", ExpiresIn: 60}}
	controller := &HandoffController{Service: service, ServiceToken: "secret"}

	recorder := httptest.NewRecorder()
	controller.MintHandoff(recorder, mintRequestHTTP("wrong"))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	recorder = httptest.NewRecorder()
	controller.MintHandoff(recorder, mintRequestHTTP(""))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	proxied := mintRequestHTTP("secret")
	proxied.Header.Set("X-Real-IP", "203.0.113.5")
	recorder = httptest.NewRecorder()
	controller.MintHandoff(recorder, proxied)
	require.Equal(t, http.StatusNotFound, recorder.Code, "never reachable through the public nginx")

	recorder = httptest.NewRecorder()
	controller.MintHandoff(recorder, mintRequestHTTP("secret"))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "https://p/auth/handoff#code=x", handoffBody(t, recorder)["url"])
	require.Equal(t, iamService.MintHandoffRequest{TenantID: "t1", ExternalUID: "fb-1", Email: "a@b.c", EmailVerified: true, ReturnPath: "/viewer"}, service.mint)
}

func TestMintIsDisabledWithoutAToken(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&HandoffController{Service: &fakeHandoffService{}}).MintHandoff(recorder, mintRequestHTTP("anything"))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestMintReportsLinkRequiredAndErrors(t *testing.T) {
	service := &fakeHandoffService{mintResult: iamService.MintHandoffResult{URL: "https://p/auth/link#code=y", ExpiresIn: 300, LinkRequired: true}}
	controller := &HandoffController{Service: service, ServiceToken: "secret"}
	recorder := httptest.NewRecorder()
	controller.MintHandoff(recorder, mintRequestHTTP("secret"))
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, "link_required", handoffBody(t, recorder)["error"])

	for err, status := range map[error]int{
		iamService.ErrHandoffNotAllowed:     http.StatusForbidden,
		iamService.ErrHandoffInvalidRequest: http.StatusBadRequest,
		iamService.ErrHandoffUnavailable:    http.StatusServiceUnavailable,
	} {
		service.err = err
		recorder = httptest.NewRecorder()
		controller.MintHandoff(recorder, mintRequestHTTP("secret"))
		require.Equal(t, status, recorder.Code, err.Error())
	}

	unknown := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"tenantId":"t1","admin":true}`))
	unknown.Header.Set("Authorization", "Bearer secret")
	recorder = httptest.NewRecorder()
	controller.MintHandoff(recorder, unknown)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestExchangeReturnsTheSession(t *testing.T) {
	service := &fakeHandoffService{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/iam/handoff/exchange", strings.NewReader(`{"code":"abc"}`))
	request.RemoteAddr = "10.1.2.3:5555"

	(&HandoffController{Service: service}).ExchangeHandoff(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	data := handoffBody(t, recorder)
	require.Equal(t, "s-1", data["sessionToken"])
	require.Equal(t, "/viewer", data["returnPath"])
	require.Equal(t, "abc", service.code)
	require.Equal(t, "10.1.2.3", service.ip)

	service.err = iamService.ErrHandoffInvalidCode
	recorder = httptest.NewRecorder()
	(&HandoffController{Service: service}).ExchangeHandoff(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"abc"}`)))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	service.err = iamService.ErrHandoffRateLimited
	recorder = httptest.NewRecorder()
	(&HandoffController{Service: service}).ExchangeHandoff(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"abc"}`)))
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "60", recorder.Header().Get("Retry-After"))
}

func TestLinkUsesTheSignedInUser(t *testing.T) {
	service := &fakeHandoffService{}
	controller := &HandoffController{Service: service}

	recorder := httptest.NewRecorder()
	controller.CompleteHandoffLink(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"abc"}`)))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"abc"}`))
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "t1")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "u1")
	recorder = httptest.NewRecorder()
	controller.CompleteHandoffLink(recorder, request.WithContext(ctx))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []string{"abc", "t1", "u1"}, []string{service.code, service.tenant, service.uid})
	require.Equal(t, "/viewer", handoffBody(t, recorder)["returnPath"])
}
