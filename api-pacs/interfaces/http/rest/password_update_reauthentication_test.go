package rest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

	"api-pacs/infrastructures/providers/api/identitytoolkit"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	apiError "api-pacs/internal/errors"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamRepository "api-pacs/module/iam/domain/repository"
	iamRepositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
	iamService "api-pacs/module/iam/infrastructure/service"
	iamServiceTypes "api-pacs/module/iam/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	userEntity "api-pacs/module/user/domain/entity"
	userRepository "api-pacs/module/user/domain/repository"
	userCommandRepository "api-pacs/module/user/infrastructure/repository"
	userRepositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	userService "api-pacs/module/user/infrastructure/service"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
	userRest "api-pacs/module/user/interfaces/http/rest"
)

const (
	passwordReauthProjectID = "demo-password-reauth"
	passwordReauthTenantID  = "tenant-a"
	passwordReauthUserID    = "user-a"
	passwordReauthOtherID   = "user-b"
	passwordReauthDecoyID   = "user-decoy"
	passwordReauthSession   = "placeholder-session"
	passwordReauthAPIKey    = "placeholder-web-api-key"

	// Obvious placeholders. The auth stand-in compares them exactly; they are
	// not credentials of any real account.
	passwordReauthCurrent     = "Placeholder-current!"
	passwordReauthOther       = "Placeholder-other!"
	passwordReauthDecoy       = "Placeholder-decoy!"
	passwordReauthReplacement = "Placeholder-replacement!"
	passwordReauthWrong       = "Placeholder-wrong!"
)

// passwordReauthIdentity is the Identity Platform stand-in the admin SDK and
// the login client both talk to. accounts:update stores a password with no
// old-password field, matching firebase.google.com/go/v4 v4.14.0
// auth/user_mgt.go Password and updateUser. accounts:signInWithPassword checks
// that stored password, matching the client in identitytoolkit.go.
type passwordReauthIdentity struct {
	projectID string
	mu        sync.Mutex
	accounts  map[string]*passwordReauthAccount
	emailSwap map[string]string
	events    []passwordReauthEvent
	paths     []string
}

type passwordReauthAccount struct {
	localID       string
	email         string
	password      string
	emailVerified bool
}

type passwordReauthEvent struct {
	kind     string
	tenantID string
	localID  string
	ok       bool
}

func newPasswordReauthIdentity(projectID string, accounts ...*passwordReauthAccount) *passwordReauthIdentity {
	store := &passwordReauthIdentity{
		projectID: projectID,
		accounts:  map[string]*passwordReauthAccount{},
		emailSwap: map[string]string{},
	}
	for _, account := range accounts {
		store.accounts[account.localID] = account
	}
	return store
}

func (store *passwordReauthIdentity) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	store.mu.Lock()
	store.paths = append(store.paths, r.URL.Path)
	store.mu.Unlock()

	switch {
	case strings.HasSuffix(r.URL.Path, "/accounts:update"):
		store.updateAccount(w, r.URL.Path, body)
	case strings.HasSuffix(r.URL.Path, "/accounts:lookup"):
		store.lookupAccount(w, body)
	case strings.HasSuffix(r.URL.Path, "/accounts:signInWithPassword"):
		store.signIn(w, body)
	default:
		http.NotFound(w, r)
	}
}

func (store *passwordReauthIdentity) updateAccount(w http.ResponseWriter, path string, body []byte) {
	var request struct {
		LocalID       string  `json:"localId"`
		Password      *string `json:"password"`
		EmailVerified *bool   `json:"emailVerified"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.LocalID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	account := store.accounts[request.LocalID]
	if account == nil {
		writeIdentityError(w, "USER_NOT_FOUND")
		return
	}
	if request.Password != nil {
		account.password = *request.Password
	}
	if request.EmailVerified != nil {
		account.emailVerified = *request.EmailVerified
	}
	store.events = append(store.events, passwordReauthEvent{
		kind:     "update",
		tenantID: tenantFromIdentityPath(path),
		localID:  request.LocalID,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"localId": account.localID})
}

func (store *passwordReauthIdentity) lookupAccount(w http.ResponseWriter, body []byte) {
	var request struct {
		LocalID []string `json:"localId"`
		Email   []string `json:"email"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	var account *passwordReauthAccount
	switch {
	case len(request.LocalID) == 1:
		account = store.accounts[request.LocalID[0]]
	case len(request.Email) == 1:
		localID := store.emailSwap[request.Email[0]]
		if localID == "" {
			localID = store.localIDByEmail(request.Email[0])
		}
		account = store.accounts[localID]
	}
	users := []map[string]any{}
	if account != nil {
		users = append(users, map[string]any{
			"localId":       account.localID,
			"email":         account.email,
			"emailVerified": account.emailVerified,
			"disabled":      false,
			"validSince":    "0",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
}

func (store *passwordReauthIdentity) signIn(w http.ResponseWriter, body []byte) {
	var request struct {
		TenantID string `json:"tenantId"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	localID := store.localIDByEmail(request.Email)
	account := store.accounts[localID]
	if account == nil {
		store.events = append(store.events, passwordReauthEvent{kind: "sign-in", tenantID: request.TenantID, ok: false})
		writeIdentityError(w, "EMAIL_NOT_FOUND")
		return
	}
	if account.password != request.Password {
		store.events = append(store.events, passwordReauthEvent{kind: "sign-in", tenantID: request.TenantID, localID: account.localID, ok: false})
		writeIdentityError(w, "INVALID_PASSWORD")
		return
	}
	// A later lookup by the same email returns a different account. The
	// sign-in response keeps the account that proved the password.
	if decoy := store.accounts[passwordReauthDecoyID]; decoy != nil && account.localID != passwordReauthDecoyID {
		store.emailSwap[account.email] = passwordReauthDecoyID
	}
	store.events = append(store.events, passwordReauthEvent{
		kind: "sign-in", tenantID: request.TenantID, localID: account.localID, ok: true,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"idToken": passwordReauthIDToken(store.projectID, request.TenantID, account.localID),
		"localId": account.localID,
		"email":   account.email,
	})
}

func (store *passwordReauthIdentity) localIDByEmail(email string) string {
	for _, account := range store.accounts {
		if account.email == email {
			return account.localID
		}
	}
	return ""
}

func (store *passwordReauthIdentity) passwordOf(localID string) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	account := store.accounts[localID]
	if account == nil {
		return ""
	}
	return account.password
}

func (store *passwordReauthIdentity) updateBoundToSessionUser() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	verified := ""
	updates := 0
	for _, event := range store.events {
		if event.kind == "sign-in" && event.ok && event.tenantID == passwordReauthTenantID {
			verified = event.localID
		}
		if event.kind == "update" {
			updates++
			if event.tenantID != passwordReauthTenantID || event.localID != passwordReauthUserID || verified != passwordReauthUserID {
				return false
			}
			verified = ""
		}
	}
	return updates == 1
}

func (store *passwordReauthIdentity) eventCounts() (updates, signIns int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, event := range store.events {
		switch event.kind {
		case "update":
			updates++
		case "sign-in":
			signIns++
		}
	}
	return updates, signIns
}

func writeIdentityError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message},
	})
}

func tenantFromIdentityPath(path string) string {
	const marker = "/tenants/"
	index := strings.Index(path, marker)
	if index < 0 {
		return ""
	}
	rest := path[index+len(marker):]
	if slash := strings.Index(rest, "/"); slash >= 0 {
		return rest[:slash]
	}
	return rest
}

func passwordReauthIDToken(projectID, tenantID, localID string) string {
	header, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	if err != nil {
		panic(err)
	}
	now := time.Now().Unix()
	payload, err := json.Marshal(map[string]any{
		"aud":       projectID,
		"iss":       "https://securetoken.google.com/" + projectID,
		"iat":       now,
		"exp":       now + 3600,
		"auth_time": now,
		"sub":       localID,
		"firebase": map[string]any{
			"identities":       map[string]any{},
			"sign_in_provider": "password",
			"tenant":           tenantID,
		},
	})
	if err != nil {
		panic(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	return encode(header) + "." + encode(payload) + "." + encode(nil)
}

// passwordReauthProfiles is the Firestore-shaped directory the password
// service can load. The second load of an account returns the decoy, so a
// check that uses the first load and an update that uses a later load act on
// different users.
type passwordReauthProfiles struct {
	userRepository.UserQueryRepositoryInterface
	mu    sync.Mutex
	users map[string]userRepositoryTypes.GetTenantUser
	reads map[string]int
}

func newPasswordReauthProfiles(users ...userRepositoryTypes.GetTenantUser) *passwordReauthProfiles {
	directory := &passwordReauthProfiles{
		users: map[string]userRepositoryTypes.GetTenantUser{},
		reads: map[string]int{},
	}
	for _, user := range users {
		directory.users[user.ID] = user
	}
	return directory
}

func (directory *passwordReauthProfiles) SelectTenantUserByID(_ context.Context, tenantID, id string) (userRepositoryTypes.GetTenantUser, error) {
	return directory.load("id:"+id, tenantID, id)
}

func (directory *passwordReauthProfiles) SelectTenantUserByEmail(_ context.Context, tenantID, email string) (userRepositoryTypes.GetTenantUser, error) {
	directory.mu.Lock()
	var id string
	for _, user := range directory.users {
		if user.TenantID == tenantID && user.Email == email {
			id = user.ID
			break
		}
	}
	directory.mu.Unlock()
	if id == "" {
		return userRepositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}
	return directory.load("email:"+email, tenantID, id)
}

func (directory *passwordReauthProfiles) load(key, tenantID, id string) (userRepositoryTypes.GetTenantUser, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[id]
	if !ok || user.TenantID != tenantID {
		return userRepositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}
	directory.reads[key]++
	if directory.reads[key] > 1 {
		user = directory.users[passwordReauthDecoyID]
	}
	if user.ID == "" || user.TenantID != tenantID {
		return userRepositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}
	return user, nil
}

func (directory *passwordReauthProfiles) SelectTenantUsers(context.Context, string) ([]userRepositoryTypes.GetTenantUser, error) {
	return nil, nil
}

func (directory *passwordReauthProfiles) SelectUserPolicyAcceptances(context.Context, string, string, []userEntity.PolicyReference) ([]userEntity.UserPolicyAcceptance, error) {
	return nil, nil
}

func (directory *passwordReauthProfiles) SelectTenantUserEmailInviteByEmail(context.Context, string, string) (userEntity.UserEmailInvite, error) {
	return userEntity.UserEmailInvite{}, errors.New(apiError.MissingRecord)
}

func (directory *passwordReauthProfiles) SelectTenantUserEmailInviteByID(context.Context, string, string) (userEntity.UserEmailInvite, error) {
	return userEntity.UserEmailInvite{}, errors.New(apiError.MissingRecord)
}

func (directory *passwordReauthProfiles) SelectTenantUserEmailInvites(context.Context, string) ([]userEntity.UserEmailInvite, error) {
	return nil, nil
}

func (directory *passwordReauthProfiles) SelectUserMetadataByID(context.Context, string) (userEntity.UserMetadata, error) {
	return userEntity.UserMetadata{}, errors.New(apiError.MissingRecord)
}

type passwordReauthSessions struct {
	iamApplication.IAMQueryServiceInterface
	iamApplication.IAMCommandServiceInterface
	token   string
	session iamEntity.TokenSession
}

func (sessions *passwordReauthSessions) GetSessionToken(_ context.Context, key string) (iamEntity.TokenSession, error) {
	if key != sessions.token {
		return iamEntity.TokenSession{}, errors.New(apiError.MissingRecord)
	}
	return sessions.session, nil
}

func (sessions *passwordReauthSessions) IsUserSuspended(context.Context, string, string) (bool, error) {
	return false, nil
}

func (sessions *passwordReauthSessions) SetTokenSession(context.Context, iamServiceTypes.SetTokenSession) error {
	return nil
}

func (sessions *passwordReauthSessions) AcquireUserAccessTransition(context.Context, string, string, string, time.Duration) (bool, error) {
	return false, errors.New(apiError.ForbiddenAccess)
}

func (sessions *passwordReauthSessions) ClearUserSuspension(context.Context, string, string) error {
	return errors.New(apiError.ForbiddenAccess)
}

func (sessions *passwordReauthSessions) ForgotTenantUserPassword(context.Context, string, string, string) error {
	return errors.New(apiError.ForbiddenAccess)
}

func (sessions *passwordReauthSessions) LoginTenantUser(context.Context, iamServiceTypes.LoginTenantUser) (string, error) {
	return "", errors.New(apiError.UnauthorizedAccess)
}

func (sessions *passwordReauthSessions) RevokeUserSessions(context.Context, string, string) error {
	return errors.New(apiError.ForbiddenAccess)
}

func (sessions *passwordReauthSessions) ReleaseUserAccessTransition(context.Context, string, string, string) error {
	return errors.New(apiError.ForbiddenAccess)
}

func (sessions *passwordReauthSessions) SetUserSuspended(context.Context, string, string) (bool, error) {
	return false, errors.New(apiError.ForbiddenAccess)
}

func (sessions *passwordReauthSessions) VerifyTenantUserEmail(context.Context, string, string, string) error {
	return errors.New(apiError.ForbiddenAccess)
}

type passwordReauthLoginUsers struct {
	userApplication.UserQueryServiceInterface
	users map[string]userServiceTypes.GetTenantUser
}

func (query *passwordReauthLoginUsers) GetTenantUserByID(_ context.Context, tenantID, id string) (userServiceTypes.GetTenantUser, error) {
	user, ok := query.users[id]
	if !ok || user.TenantID != tenantID {
		return userServiceTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}
	return user, nil
}

type passwordReauthLoginProtection struct {
	iamApplication.LoginAbuseProtectionInterface
}

func (protection *passwordReauthLoginProtection) EvaluateLoginAttempt(context.Context, iamServiceTypes.LoginAbuseSignals) (iamServiceTypes.LoginProtectionDecision, error) {
	return iamServiceTypes.LoginProtectionDecision{}, nil
}

func (protection *passwordReauthLoginProtection) RecordLoginFailure(context.Context, iamServiceTypes.LoginAbuseSignals) (iamServiceTypes.LoginProtectionDecision, error) {
	return iamServiceTypes.LoginProtectionDecision{}, nil
}

func (protection *passwordReauthLoginProtection) ResetAccountFailures(context.Context, iamServiceTypes.LoginAbuseSignals) error {
	return nil
}

type passwordReauthLoginRepository struct {
	iamRepository.IAMCommandRepositoryInterface
	sessions []iamRepositoryTypes.SetTokenSession
}

func (repository *passwordReauthLoginRepository) SetTokenSession(data iamRepositoryTypes.SetTokenSession) error {
	repository.sessions = append(repository.sessions, data)
	return nil
}

func (repository *passwordReauthLoginRepository) DeleteTokenSession(string) error { return nil }

func (repository *passwordReauthLoginRepository) RevokeUserSessions(string, string) error { return nil }

func (repository *passwordReauthLoginRepository) ClearUserSuspension(string, string) error {
	return nil
}

func (repository *passwordReauthLoginRepository) ReleaseUserAccessTransition(string, string, string) error {
	return nil
}

func (repository *passwordReauthLoginRepository) SetUserSuspended(string, string) (bool, error) {
	return true, nil
}

func (repository *passwordReauthLoginRepository) AcquireUserAccessTransition(string, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (repository *passwordReauthLoginRepository) IncrementPublicEmailRequests(string, time.Duration) (int64, error) {
	return 0, nil
}

func (repository *passwordReauthLoginRepository) IsEmailCooldownActive(string) (bool, error) {
	return false, nil
}

func (repository *passwordReauthLoginRepository) SetEmailCooldown(string) error { return nil }

type passwordReauthHarness struct {
	router  http.Handler
	store   *passwordReauthIdentity
	login   *iamService.IAMCommandService
	session string
}

func newPasswordReauthHarness(t *testing.T, role string, adminCreated bool) *passwordReauthHarness {
	t.Helper()
	store := newPasswordReauthIdentity(passwordReauthProjectID,
		&passwordReauthAccount{localID: passwordReauthUserID, email: "victim@example.com", password: passwordReauthCurrent, emailVerified: true},
		&passwordReauthAccount{localID: passwordReauthOtherID, email: "other@example.com", password: passwordReauthOther, emailVerified: true},
		&passwordReauthAccount{localID: passwordReauthDecoyID, email: "decoy@example.com", password: passwordReauthDecoy, emailVerified: true},
	)
	server := httptest.NewServer(store)
	t.Cleanup(server.Close)
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", strings.TrimPrefix(server.URL, "http://"))

	app, err := firebase.NewApp(t.Context(), &firebase.Config{ProjectID: passwordReauthProjectID})
	require.NoError(t, err)
	repository := &userCommandRepository.UserCommandRepository{
		FirebaseAdminSDK: &firebaseadmin.FirebaseAdminSDK{App: app},
	}
	profiles := newPasswordReauthProfiles(
		passwordReauthProfile(passwordReauthUserID, "victim@example.com", role, adminCreated),
		passwordReauthProfile(passwordReauthOtherID, "other@example.com", iamEntity.UserRole, false),
		passwordReauthProfile(passwordReauthDecoyID, "decoy@example.com", iamEntity.UserRole, false),
	)
	identityAPI := &identitytoolkit.IdentityToolkitAPI{
		APIKey:  passwordReauthAPIKey,
		BaseURL: server.URL + "/identitytoolkit.googleapis.com/v1",
		Client:  server.Client(),
	}
	loginUsers := map[string]userServiceTypes.GetTenantUser{
		passwordReauthUserID:  passwordReauthLoginProfile(passwordReauthUserID, "victim@example.com", role, adminCreated),
		passwordReauthOtherID: passwordReauthLoginProfile(passwordReauthOtherID, "other@example.com", iamEntity.UserRole, false),
		passwordReauthDecoyID: passwordReauthLoginProfile(passwordReauthDecoyID, "decoy@example.com", iamEntity.UserRole, false),
	}
	loginRepository := &passwordReauthLoginRepository{}
	loginService := &iamService.IAMCommandService{
		IAMCommandRepositoryInterface:  loginRepository,
		UserQueryServiceInterface:      &passwordReauthLoginUsers{users: loginUsers},
		TenantIDTokenVerifierInterface: &firebaseadmin.FirebaseAdminSDK{App: app},
		IdentityToolkitAPIInterface:    identityAPI,
		LoginAbuseProtectionInterface:  &passwordReauthLoginProtection{},
	}
	commandService := &userService.UserCommandService{
		UserCommandRepositoryInterface: &userCommandRepository.UserCommandRepositoryCircuitBreaker{
			UserCommandRepositoryInterface: repository,
		},
		UserQueryRepositoryInterface: profiles,
		IAMCommandServiceInterface:   loginService,
	}
	controller := userRest.UserCommandController{UserCommandServiceInterface: commandService}
	sessions := &passwordReauthSessions{
		token: passwordReauthSession,
		session: iamEntity.TokenSession{
			TenantID:   passwordReauthTenantID,
			UserID:     passwordReauthUserID,
			Role:       role,
			Generation: iamEntity.SessionGeneration,
		},
	}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMQueryServiceInterface:   sessions,
		IAMCommandServiceInterface: sessions,
	}
	router := chi.NewRouter()
	router.Route("/v1", func(r chi.Router) {
		r.Use(requestbody.Limit(requestbody.DefaultAPIMaxBytes))
		r.Route("/user", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			r.Put("/password/update", controller.UpdateTenantUserPassword)
		})
	})
	return &passwordReauthHarness{router: router, store: store, login: loginService, session: passwordReauthSession}
}

func passwordReauthProfile(id, email, role string, adminCreated bool) userRepositoryTypes.GetTenantUser {
	return userRepositoryTypes.GetTenantUser{
		ID: id, TenantID: passwordReauthTenantID, Role: role, Email: email,
		Name: "Synthetic User", LicenseNo: "LIC-" + id, Specialty: "Cardiology",
		AccessState: userEntity.AccountAccessActive, IsEmailVerified: true, IsAdminCreated: adminCreated,
	}
}

func passwordReauthLoginProfile(id, email, role string, adminCreated bool) userServiceTypes.GetTenantUser {
	return userServiceTypes.GetTenantUser{
		ID: id, TenantID: passwordReauthTenantID, Role: role, Email: email,
		Name: "Synthetic User", LicenseNo: "LIC-" + id, Specialty: "Cardiology",
		AccessState: userEntity.AccountAccessActive, IsEmailVerified: true, IsAdminCreated: adminCreated,
	}
}

func (harness *passwordReauthHarness) updatePassword(t *testing.T, body map[string]string) (int, bool) {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPut, "/v1/user/password/update", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+harness.session)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	harness.router.ServeHTTP(recorder, request)
	var payload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return recorder.Code, payload.Success
}

func (harness *passwordReauthHarness) loginAs(email, password string) (string, error) {
	return harness.login.LoginTenantUser(context.Background(), iamServiceTypes.LoginTenantUser{
		TenantID: passwordReauthTenantID,
		Email:    email,
		Password: password,
		ClientIP: "203.0.113.10",
	})
}

func (harness *passwordReauthHarness) requirePassword(t *testing.T, localID, want string) {
	t.Helper()
	if harness.store.passwordOf(localID) != want {
		t.Fatalf("account %s password does not match the expected placeholder", localID)
	}
}

func TestPasswordLoginHarnessAcceptsCurrentPassword(t *testing.T) {
	harness := newPasswordReauthHarness(t, iamEntity.UserRole, false)

	session, err := harness.loginAs("victim@example.com", passwordReauthCurrent)

	require.NoError(t, err)
	require.NotEmpty(t, session)
	_, err = harness.loginAs("victim@example.com", passwordReauthReplacement)
	require.Error(t, err)
}

func TestPasswordUpdateWithoutCurrentPasswordLeavesCredentialsAndLoginUnchanged(t *testing.T) {
	for _, role := range []string{iamEntity.UserRole, iamEntity.AdminRole, iamEntity.OwnerRole} {
		t.Run(role, func(t *testing.T) {
			harness := newPasswordReauthHarness(t, role, false)

			status, success := harness.updatePassword(t, map[string]string{
				"newPassword": passwordReauthReplacement,
				"id":          passwordReauthOtherID,
			})

			require.NotEqual(t, http.StatusOK, status)
			require.False(t, success)
			harness.requirePassword(t, passwordReauthUserID, passwordReauthCurrent)
			harness.requirePassword(t, passwordReauthOtherID, passwordReauthOther)
			harness.requirePassword(t, passwordReauthDecoyID, passwordReauthDecoy)
			session, err := harness.loginAs("victim@example.com", passwordReauthCurrent)
			require.NoError(t, err)
			require.NotEmpty(t, session)
			_, err = harness.loginAs("victim@example.com", passwordReauthReplacement)
			require.Error(t, err)
		})
	}
}

func TestPasswordUpdateWithoutCurrentPasswordLeavesAdminCreatedAccountUnchanged(t *testing.T) {
	harness := newPasswordReauthHarness(t, iamEntity.UserRole, true)

	status, success := harness.updatePassword(t, map[string]string{
		"newPassword": passwordReauthReplacement,
	})

	require.NotEqual(t, http.StatusOK, status)
	require.False(t, success)
	harness.requirePassword(t, passwordReauthUserID, passwordReauthCurrent)
	_, err := harness.loginAs("victim@example.com", passwordReauthReplacement)
	require.Error(t, err)
}

func TestPasswordUpdateRejectsUnprovenCurrentPassword(t *testing.T) {
	tests := []struct {
		name string
		body map[string]string
	}{
		{
			name: "wrong current password",
			body: map[string]string{
				"currentPassword": passwordReauthWrong,
				"newPassword":     passwordReauthReplacement,
			},
		},
		{
			name: "other account password and id",
			body: map[string]string{
				"currentPassword": passwordReauthOther,
				"newPassword":     passwordReauthReplacement,
				"id":              passwordReauthOtherID,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newPasswordReauthHarness(t, iamEntity.UserRole, false)

			status, success := harness.updatePassword(t, test.body)

			require.NotEqual(t, http.StatusOK, status)
			require.False(t, success)
			harness.requirePassword(t, passwordReauthUserID, passwordReauthCurrent)
			harness.requirePassword(t, passwordReauthOtherID, passwordReauthOther)
			harness.requirePassword(t, passwordReauthDecoyID, passwordReauthDecoy)
			_, err := harness.loginAs("victim@example.com", passwordReauthReplacement)
			require.Error(t, err)
			_, err = harness.loginAs("other@example.com", passwordReauthReplacement)
			require.Error(t, err)
		})
	}
}

func TestPasswordUpdateWithCurrentPasswordChangesOnlyTheAuthenticatedUser(t *testing.T) {
	harness := newPasswordReauthHarness(t, iamEntity.OwnerRole, false)

	status, success := harness.updatePassword(t, map[string]string{
		"currentPassword": passwordReauthCurrent,
		"newPassword":     passwordReauthReplacement,
		"id":              passwordReauthOtherID,
	})

	require.Equal(t, http.StatusOK, status)
	require.True(t, success)
	if !harness.store.updateBoundToSessionUser() {
		updates, signIns := harness.store.eventCounts()
		t.Fatalf("password change for %s was not bound to a prior sign-in of that same user (updates=%d signIns=%d paths=%v)", passwordReauthUserID, updates, signIns, harness.store.paths)
	}
	harness.requirePassword(t, passwordReauthUserID, passwordReauthReplacement)
	harness.requirePassword(t, passwordReauthOtherID, passwordReauthOther)
	harness.requirePassword(t, passwordReauthDecoyID, passwordReauthDecoy)

	session, err := harness.loginAs("victim@example.com", passwordReauthReplacement)
	require.NoError(t, err)
	require.NotEmpty(t, session)
	_, err = harness.loginAs("victim@example.com", passwordReauthCurrent)
	require.Error(t, err)
	_, err = harness.loginAs("other@example.com", passwordReauthOther)
	require.NoError(t, err)
}
