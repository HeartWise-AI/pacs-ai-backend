package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	firebase "firebase.google.com/go/v4"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	mailgunsdk "api-pacs/infrastructures/providers/sdk/mailgun"
	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	iamRepository "api-pacs/module/iam/infrastructure/repository"
	iamService "api-pacs/module/iam/infrastructure/service"
)

const (
	forgotPasswordProjectID  = "pacs-ai-test"
	forgotPasswordTenantID   = "tenant-a"
	forgotPasswordSender     = "password-reset@example.test"
	forgotPasswordMailKey    = "placeholder-mailgun-key"
	forgotPasswordMailDomain = "example.test"

	verifiedForgotPasswordEmail   = "verified.clinician@example.com"
	unverifiedForgotPasswordEmail = "unverified.clinician@example.com"
	missingForgotPasswordEmail    = "missing.clinician@example.com"
)

type forgotPasswordResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
}

func genericForgotPasswordResponse() forgotPasswordResponse {
	return forgotPasswordResponse{
		Success: true,
		Message: "Successfully sent forgot password request.",
	}
}

func genericVerifyEmailResponse() forgotPasswordResponse {
	return forgotPasswordResponse{
		Success: true,
		Message: "Successfully sent verify email request.",
	}
}

// memoryRedis follows RedisDBHandler.Get: a missing key returns the error
// string "empty", which IAMCommandRepository treats as an inactive cooldown.
type memoryRedis struct {
	mu    sync.Mutex
	items map[string]string
}

func (store *memoryRedis) Delete(string) error { return nil }

func (store *memoryRedis) DeleteIfValueMatches(string, interface{}) (bool, error) {
	return false, nil
}

func (store *memoryRedis) Flush() {}

func (store *memoryRedis) Get(key string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.items[key]
	if !ok {
		return "", errors.New("empty")
	}
	return value, nil
}

func (store *memoryRedis) GetCounterWithExpiry(string) (int64, time.Duration, error) {
	return 0, 0, nil
}

func (store *memoryRedis) IncrementWithExpiry(string, time.Duration) (int64, time.Duration, error) {
	return 0, 0, nil
}

func (store *memoryRedis) Scan(string) ([]string, error) { return nil, nil }

func (store *memoryRedis) Set(key string, val interface{}, _ time.Duration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.items == nil {
		store.items = map[string]string{}
	}
	store.items[key] = fmt.Sprint(val)
	return nil
}

func (store *memoryRedis) SetIfAbsent(string, interface{}, time.Duration) (bool, error) {
	return false, nil
}

func (store *memoryRedis) SetIfKeyAbsent(string, string, interface{}, time.Duration) (bool, error) {
	return false, nil
}

type identityCall struct {
	Path string
	Body string
}

type identityBackend struct {
	mu    sync.Mutex
	users map[string]bool
	calls []identityCall
}

func (backend *identityBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	backend.mu.Lock()
	backend.calls = append(backend.calls, identityCall{Path: r.URL.Path, Body: string(body)})
	backend.mu.Unlock()

	switch {
	case strings.Contains(r.URL.Path, "accounts:lookup"):
		backend.lookup(w, body)
	case strings.Contains(r.URL.Path, "accounts:sendOobCode"):
		backend.sendOobCode(w, body)
	default:
		http.NotFound(w, r)
	}
}

func (backend *identityBackend) lookup(w http.ResponseWriter, body []byte) {
	var request struct {
		Email []string `json:"email"`
	}
	if err := json.Unmarshal(body, &request); err != nil || len(request.Email) != 1 {
		http.Error(w, "bad lookup", http.StatusBadRequest)
		return
	}
	verified, found := backend.users[request.Email[0]]
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"users": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users": []map[string]any{{
			"localId":       "user-1",
			"email":         request.Email[0],
			"emailVerified": verified,
		}},
	})
}

func (backend *identityBackend) sendOobCode(w http.ResponseWriter, body []byte) {
	var request struct {
		Email       string `json:"email"`
		RequestType string `json:"requestType"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Email == "" {
		http.Error(w, "bad oob", http.StatusBadRequest)
		return
	}
	if _, found := backend.users[request.Email]; !found {
		// firebase-admin-go v4.14.0 maps this Identity Toolkit body to email-not-found.
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": "EMAIL_NOT_FOUND"},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"oobLink": "https://example.test/action?email=" + request.Email + "&type=" + request.RequestType,
	})
}

func (backend *identityBackend) callsMatching(fragment string) []identityCall {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	var matched []identityCall
	for _, call := range backend.calls {
		if strings.Contains(call.Path, fragment) {
			matched = append(matched, call)
		}
	}
	return matched
}

type sentMessage struct {
	Recipient string
	Subject   string
	HTML      string
}

type mailBackend struct {
	mu       sync.Mutex
	messages []sentMessage
}

func (backend *mailBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "bad mail form", http.StatusBadRequest)
		return
	}
	message := sentMessage{
		Recipient: r.FormValue("to"),
		Subject:   r.FormValue("subject"),
		HTML:      r.FormValue("html"),
	}
	backend.mu.Lock()
	backend.messages = append(backend.messages, message)
	backend.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Queued. Thank you.",
		"id":      "<placeholder@example.test>",
	})
}

func (backend *mailBackend) snapshot() []sentMessage {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	out := make([]sentMessage, len(backend.messages))
	copy(out, backend.messages)
	return out
}

func (backend *mailBackend) to(recipient string) []sentMessage {
	var matched []sentMessage
	for _, message := range backend.snapshot() {
		if message.Recipient == recipient {
			matched = append(matched, message)
		}
	}
	return matched
}

type forgotPasswordHarness struct {
	router   http.Handler
	identity *identityBackend
	mail     *mailBackend
}

func newForgotPasswordHarness(t *testing.T, users map[string]bool) *forgotPasswordHarness {
	t.Helper()
	identity := &identityBackend{users: users}
	identityServer := httptest.NewServer(identity)
	t.Cleanup(identityServer.Close)
	mail := &mailBackend{}
	mailServer := httptest.NewServer(mail)
	t.Cleanup(mailServer.Close)

	// firebase-admin-go v4.14.0 sends Auth RPCs to this host when the emulator
	// variable is set. The request path and response parsing stay the production code.
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", strings.TrimPrefix(identityServer.URL, "http://"))
	t.Setenv("MAILGUN_SENDER_EMAIL", forgotPasswordSender)

	app, err := firebase.NewApp(context.Background(), &firebase.Config{ProjectID: forgotPasswordProjectID}, option.WithoutAuthentication())
	require.NoError(t, err)
	mailClient, err := mailgunsdk.NewMailgun(mailgunTypes.Config{
		Domain: forgotPasswordMailDomain,
		APIKey: forgotPasswordMailKey,
	})
	require.NoError(t, err)
	mailClient.MailgunImpl.SetAPIBase(mailServer.URL + "/v3")

	service := &iamService.IAMCommandService{
		IAMCommandRepositoryInterface: &iamRepository.IAMCommandRepository{
			RedisDBHandlerInterface: &memoryRedis{},
		},
		FirebaseAdminSDK: &firebaseadmin.FirebaseAdminSDK{App: app},
		MailgunSDK:       mailClient,
	}
	controller := &IAMCommandController{IAMCommandServiceInterface: service}

	router := chi.NewRouter()
	router.Route("/v1", func(r chi.Router) {
		r.Use(requestbody.Limit(requestbody.DefaultAPIMaxBytes))
		r.Route("/iam", func(r chi.Router) {
			r.Post("/forgot-password", controller.ForgotTenantUserPassword)
			r.Post("/verify-email", controller.VerifyTenantUserEmail)
		})
	})

	return &forgotPasswordHarness{router: router, identity: identity, mail: mail}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func postPublicIAM(t *testing.T, router http.Handler, path, email string) (int, forgotPasswordResponse) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"tenantId": forgotPasswordTenantID,
		"email":    email,
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(payload)))
	request.RemoteAddr = "203.0.113.25:4242"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	var response forgotPasswordResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder.Code, response
}

func TestForgotPasswordUsesOneResponseForEveryAccountState(t *testing.T) {
	harness := newForgotPasswordHarness(t, map[string]bool{
		verifiedForgotPasswordEmail:   true,
		unverifiedForgotPasswordEmail: false,
	})

	verifiedStatus, verifiedResponse := postPublicIAM(t, harness.router, "/v1/iam/forgot-password", verifiedForgotPasswordEmail)
	unverifiedStatus, unverifiedResponse := postPublicIAM(t, harness.router, "/v1/iam/forgot-password", unverifiedForgotPasswordEmail)
	missingStatus, missingResponse := postPublicIAM(t, harness.router, "/v1/iam/forgot-password", missingForgotPasswordEmail)

	type observed struct {
		Status int
		Body   forgotPasswordResponse
	}
	generic := observed{Status: http.StatusOK, Body: genericForgotPasswordResponse()}
	require.Equal(t, []observed{generic, generic, generic}, []observed{
		{Status: verifiedStatus, Body: verifiedResponse},
		{Status: unverifiedStatus, Body: unverifiedResponse},
		{Status: missingStatus, Body: missingResponse},
	})

	verifiedMail := harness.mail.to(verifiedForgotPasswordEmail)
	require.Len(t, verifiedMail, 1)
	require.Contains(t, verifiedMail[0].HTML, "https://example.test/action?email="+verifiedForgotPasswordEmail+"&type=PASSWORD_RESET")
	require.Empty(t, harness.mail.to(unverifiedForgotPasswordEmail))
	require.Empty(t, harness.mail.to(missingForgotPasswordEmail))

	lookups := harness.identity.callsMatching("accounts:lookup")
	require.NotEmpty(t, lookups)
	for _, call := range lookups {
		require.Contains(t, call.Path, "/tenants/"+forgotPasswordTenantID+"/")
	}
	resetLinks := harness.identity.callsMatching("accounts:sendOobCode")
	require.Len(t, resetLinks, 1)
	require.Contains(t, resetLinks[0].Body, `"requestType":"PASSWORD_RESET"`)
	require.Contains(t, resetLinks[0].Body, verifiedForgotPasswordEmail)
}

func TestForgotPasswordSendsAtMostOneResetEmailWithoutAnotherLink(t *testing.T) {
	harness := newForgotPasswordHarness(t, map[string]bool{
		verifiedForgotPasswordEmail: true,
	})

	firstStatus, firstResponse := postPublicIAM(t, harness.router, "/v1/iam/forgot-password", verifiedForgotPasswordEmail)
	secondStatus, secondResponse := postPublicIAM(t, harness.router, "/v1/iam/forgot-password", verifiedForgotPasswordEmail)

	require.Equal(t, http.StatusOK, firstStatus)
	require.Equal(t, genericForgotPasswordResponse(), firstResponse)
	require.Equal(t, firstStatus, secondStatus)
	require.Equal(t, firstResponse, secondResponse)
	require.Len(t, harness.mail.to(verifiedForgotPasswordEmail), 1)
	require.Len(t, harness.identity.callsMatching("accounts:sendOobCode"), 1)
}

func TestVerifyEmailUsesOneResponseForExistingAndMissingAccounts(t *testing.T) {
	const existing = "existing.clinician@example.com"
	harness := newForgotPasswordHarness(t, map[string]bool{
		existing: true,
	})

	existingStatus, existingResponse := postPublicIAM(t, harness.router, "/v1/iam/verify-email", existing)
	missingStatus, missingResponse := postPublicIAM(t, harness.router, "/v1/iam/verify-email", missingForgotPasswordEmail)
	require.Equal(t, http.StatusOK, existingStatus)
	require.Equal(t, genericVerifyEmailResponse(), existingResponse)
	require.Equal(t, existingStatus, missingStatus)
	require.Equal(t, existingResponse, missingResponse)

	secondExistingStatus, secondExistingResponse := postPublicIAM(t, harness.router, "/v1/iam/verify-email", existing)
	secondMissingStatus, secondMissingResponse := postPublicIAM(t, harness.router, "/v1/iam/verify-email", missingForgotPasswordEmail)
	require.Equal(t, secondExistingStatus, secondMissingStatus)
	require.Equal(t, secondExistingResponse, secondMissingResponse)

	require.Len(t, harness.mail.to(existing), 1)
	require.Empty(t, harness.mail.to(missingForgotPasswordEmail))
	verificationLinks := 0
	for _, call := range harness.identity.callsMatching("accounts:sendOobCode") {
		if strings.Contains(call.Body, `"requestType":"VERIFY_EMAIL"`) && strings.Contains(call.Body, existing) {
			verificationLinks++
		}
	}
	require.Equal(t, 1, verificationLinks)
}
