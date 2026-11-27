package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/segmentio/ksuid"

	cloudflareAPITypes "api-pacs/infrastructures/providers/api/cloudflare/types"
	identitytoolkit "api-pacs/infrastructures/providers/api/identitytoolkit"
	identitytoolkitTypes "api-pacs/infrastructures/providers/api/identitytoolkit/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/infrastructures/providers/sdk/mailgun"
	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	iamApplication "api-pacs/module/iam/application"
	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/iam/domain/repository"
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
	"api-pacs/module/iam/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

// IAMCommandService handles the IAM command service logic
type IAMCommandService struct {
	repository.IAMCommandRepositoryInterface
	userApplication.UserQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	tenantApplication.TenantQueryServiceInterface
	FirebaseAdminSDK               *firebaseadmin.FirebaseAdminSDK
	TenantIDTokenVerifierInterface firebaseadmin.TenantIDTokenVerifierInterface
	IdentityToolkitAPIInterface    identitytoolkitTypes.IdentityToolkitAPIInterface
	LoginTurnstileAPIInterface     cloudflareAPITypes.LoginTurnstileAPIInterface
	LoginAbuseProtectionInterface  iamApplication.LoginAbuseProtectionInterface
	LoginTurnstileAllowedHostnames map[string]struct{}
	// LoginChallengeMode is turnstile (the zero value) or cooldown.
	LoginChallengeMode LoginChallengeMode
	MailgunSDK         *mailgun.MailgunSDK
}

const (
	publicEmailWindow        = time.Hour
	publicEmailIPRequests    = 30
	publicEmailInboxRequests = 5
	// publicEmailResponseTime is how long every request that passes the IP
	// limit takes, whether or not an email is sent, so the response time does
	// not reveal the account state. Slower sends finish after the response.
	publicEmailResponseTime = time.Second
	publicEmailSendTimeout  = 30 * time.Second
)

// ForgotTenantUserPassword emails a password reset link to a verified account.
// The response is the same whether or not the account exists or is verified.
func (service *IAMCommandService) ForgotTenantUserPassword(ctx context.Context, tenantID, email, clientIP string) error {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	return service.handlePublicEmailRequest(ctx, "password_reset", tenantID, normalizedEmail, clientIP, service.sendPasswordResetEmail)
}

// handlePublicEmailRequest rate-limits the unauthenticated password reset and
// email verification endpoints before any account lookup, then sends in the
// background so the outcome never depends on the account.
func (service *IAMCommandService) handlePublicEmailRequest(
	ctx context.Context,
	kind, tenantID, email, clientIP string,
	send func(ctx context.Context, tenantID, email string),
) error {
	ipRequests, err := service.IAMCommandRepositoryInterface.IncrementPublicEmailRequests(publicEmailRateLimitKey("ip", clientIP), publicEmailWindow)
	if err != nil {
		return err
	}
	if ipRequests > publicEmailIPRequests {
		log.Printf("[security] event=public_email_rate_limited kind=%s scope=ip", kind)
		return errors.New(apiError.MaximumLimitReached)
	}

	admitted, err := service.admitPublicEmail(kind, tenantID, email)
	if err != nil {
		return err
	}
	if admitted {
		sendContext := context.WithoutCancel(ctx)
		go func() {
			sendContext, cancel := context.WithTimeout(sendContext, publicEmailSendTimeout)
			defer cancel()
			send(sendContext, tenantID, email)
		}()
	}

	time.Sleep(publicEmailResponseTime)
	return nil
}

// admitPublicEmail applies the per-address cooldown and the atomic per-inbox
// cap. The cap bounds concurrent requests that all observe an idle cooldown.
func (service *IAMCommandService) admitPublicEmail(kind, tenantID, email string) (bool, error) {
	cooldownKey := fmt.Sprintf("%s:%s:%s", kind, tenantID, email)
	cooldownActive, err := service.IAMCommandRepositoryInterface.IsEmailCooldownActive(cooldownKey)
	if err != nil {
		return false, err
	}
	if cooldownActive {
		return false, nil
	}
	if err := service.IAMCommandRepositoryInterface.SetEmailCooldown(cooldownKey); err != nil {
		return false, err
	}

	inboxRequests, err := service.IAMCommandRepositoryInterface.IncrementPublicEmailRequests(publicEmailRateLimitKey("inbox", email), publicEmailWindow)
	if err != nil {
		return false, err
	}
	if inboxRequests > publicEmailInboxRequests {
		log.Printf("[security] event=public_email_rate_limited kind=%s scope=inbox", kind)
		return false, nil
	}
	return true, nil
}

func publicEmailRateLimitKey(scope, value string) string {
	digest := sha256.Sum256([]byte(value))
	return "pacs-ai:public-email-rate-limit:" + scope + ":" + hex.EncodeToString(digest[:])
}

func (service *IAMCommandService) sendPasswordResetEmail(ctx context.Context, tenantID, email string) {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return
	}

	userEmail, err := tenantAuth.GetUserByEmail(ctx, email)
	if err != nil {
		log.Println(err)
		return
	}

	// check if email is verified
	if !userEmail.EmailVerified {
		log.Println("[error] email not verified.")
		return
	}

	resetLink, err := tenantAuth.PasswordResetLink(ctx, email)
	if err != nil {
		log.Println(err)
		return
	}

	// send to email
	textMessage := fmt.Sprintf("Hello,<br /><br />"+
		"Follow this link to reset your PACS AI password for your %s account:<br /><br />"+
		"%s<br /><br />"+
		"If you didn’t ask to reset your password, you can ignore this email.<br /><br />"+
		"Thanks,<br />"+
		"Your PACS AI team", email, resetLink)
	err = service.MailgunSDK.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Reset password",
		Recipient:     email,
		PlainTextBody: textMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification code via mailgun", err)
	}
}

// LoginTenantUser authenticates credentials through Identity Platform and only
// creates a PACS session after the server-owned adaptive policy is satisfied.
func (service *IAMCommandService) LoginTenantUser(ctx context.Context, data types.LoginTenantUser) (string, error) {
	data.TenantID = strings.TrimSpace(data.TenantID)
	data.Email = normalizedLoginEmail(data.Email)
	data.TurnstileToken = strings.TrimSpace(data.TurnstileToken)
	signals := types.LoginAbuseSignals{
		TenantID: data.TenantID,
		Email:    data.Email,
		ClientIP: strings.TrimSpace(data.ClientIP),
	}

	decision, err := service.evaluateLoginAttempt(ctx, signals)
	if err != nil {
		return "", newLoginError(apiError.LoginProtectionUnavailable, false, 0)
	}
	if decision.RetryAfter > 0 {
		return "", newLoginRateLimitError(decision.RetryAfter)
	}
	if decision.ChallengeRequired {
		if service.loginCooldownMode() || data.TurnstileToken == "" {
			// In cooldown mode a token is never accepted in place of waiting.
			log.Printf("[security] event=login_challenge_required tenant_id=%s mode=%s", data.TenantID, service.loginChallengeMode())
			return "", service.handleDeniedLogin(ctx, signals, apiError.LoginChallengeRequired)
		}
		if err := service.validateLoginTurnstile(ctx, data.TurnstileToken, signals); err != nil {
			return "", err
		}
	}

	if service.IdentityToolkitAPIInterface == nil {
		log.Printf("[security] event=login_identity_provider_unavailable reason=not_configured")
		return "", newLoginError(apiError.FirebaseAuthError, decision.ChallengeRequired, 0)
	}
	signInResponse, err := service.IdentityToolkitAPIInterface.SignInWithPassword(ctx, identitytoolkitTypes.SignInWithPasswordRequest{
		TenantID: data.TenantID,
		Email:    data.Email,
		Password: data.Password,
	})
	if err != nil {
		if errors.Is(err, identitytoolkit.ErrCredentialsRejected) {
			return "", service.handleRejectedLogin(ctx, signals)
		}
		if errors.Is(err, identitytoolkit.ErrTooManyAttempts) {
			return "", service.handleProviderLockout(ctx, signals)
		}
		log.Printf("[security] event=login_identity_provider_unavailable tenant_id=%s reason=%s",
			data.TenantID, identitytoolkit.FailureReason(err))
		return "", newLoginError(apiError.FirebaseAuthError, decision.ChallengeRequired, 0)
	}
	if service.TenantIDTokenVerifierInterface == nil {
		log.Printf("[security] event=login_identity_provider_unavailable reason=verifier_not_configured")
		return "", newLoginError(apiError.FirebaseAuthError, decision.ChallengeRequired, 0)
	}
	userID, err := service.TenantIDTokenVerifierInterface.VerifyTenantIDToken(ctx, data.TenantID, signInResponse.IDToken)
	if err != nil || userID == "" || userID != signInResponse.LocalID {
		log.Printf("[security] event=login_identity_token_verification_failed tenant_id=%s", data.TenantID)
		return "", newLoginError(apiError.FirebaseAuthError, decision.ChallengeRequired, 0)
	}
	if normalizedLoginEmail(signInResponse.Email) != data.Email {
		log.Printf("[security] event=login_identity_mismatch tenant_id=%s", data.TenantID)
		return "", newLoginError(apiError.FirebaseAuthError, decision.ChallengeRequired, 0)
	}

	user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, data.TenantID, userID)
	if err != nil {
		log.Printf("[security] event=login_user_profile_unavailable tenant_id=%s", data.TenantID)
		return "", newLoginError(apiError.FirebaseAuthError, decision.ChallengeRequired, 0)
	}
	if !user.IsEmailVerified && !user.IsAdminCreated {
		return "", newLoginError(apiError.FirebaseAuthEmailNotVerified, decision.ChallengeRequired, 0)
	}
	if user.IsAccountDisabled || user.AccessState == "SUSPENDED" {
		return "", newLoginError(apiError.AccountSuspended, decision.ChallengeRequired, 0)
	}

	sessionToken := generateID()
	err = service.SetTokenSession(ctx, types.SetTokenSession{
		SessionID:           sessionToken,
		TenantID:            data.TenantID,
		UserID:              user.ID,
		Role:                user.Role,
		ExpireTimeInSeconds: entity.ExpireTimeInSeconds,
	})
	if err != nil {
		if err.Error() == apiError.AccountSuspended {
			return "", newLoginError(apiError.AccountSuspended, decision.ChallengeRequired, 0)
		}
		return "", newLoginError(apiError.LoginProtectionUnavailable, decision.ChallengeRequired, 0)
	}
	if err := service.resetLoginAccountFailures(ctx, signals); err != nil {
		if rollbackErr := service.IAMCommandRepositoryInterface.DeleteTokenSession(sessionToken); rollbackErr != nil {
			log.Printf("[security] event=login_session_rollback_failed tenant_id=%s", data.TenantID)
		}
		return "", newLoginError(apiError.LoginProtectionUnavailable, decision.ChallengeRequired, 0)
	}

	service.logSuccessfulLogin(ctx, sessionToken, data.TenantID, user)
	log.Printf("[security] event=login_success tenant_id=%s challenged=%t", data.TenantID, decision.ChallengeRequired)
	return sessionToken, nil
}

func (service *IAMCommandService) evaluateLoginAttempt(ctx context.Context, signals types.LoginAbuseSignals) (types.LoginProtectionDecision, error) {
	if service.LoginAbuseProtectionInterface == nil {
		log.Printf("[security] event=login_protection_unavailable operation=evaluate reason=not_configured")
		return types.LoginProtectionDecision{}, errors.New(apiError.LoginProtectionUnavailable)
	}
	return service.LoginAbuseProtectionInterface.EvaluateLoginAttempt(ctx, signals)
}

func (service *IAMCommandService) handleRejectedLogin(ctx context.Context, signals types.LoginAbuseSignals) error {
	if service.LoginAbuseProtectionInterface == nil {
		return newLoginError(apiError.LoginProtectionUnavailable, false, 0)
	}
	decision, err := service.LoginAbuseProtectionInterface.RecordLoginFailure(ctx, signals)
	if err != nil {
		return newLoginError(apiError.LoginProtectionUnavailable, false, 0)
	}
	cooldown := service.loginCooldownMode() && decision.ChallengeRequired
	log.Printf("[security] event=login_credentials_rejected tenant_id=%s challenge_required=%t rate_limited=%t mode=%s",
		signals.TenantID, decision.ChallengeRequired, decision.RetryAfter > 0 || cooldown, service.loginChallengeMode())
	if decision.RetryAfter > 0 {
		return newLoginRateLimitError(decision.RetryAfter)
	}
	if cooldown {
		return newLoginRateLimitError(loginCooldownRetryAfter(decision))
	}
	return newLoginError(apiError.UnauthorizedAccess, decision.ChallengeRequired, 0)
}

// providerLockoutRetryAfter is the Retry-After advertised when Firebase locks
// password sign-in for an account (TOO_MANY_ATTEMPTS_TRY_LATER). Firebase does
// not report how long its lockout lasts; five minutes is a conservative backoff
// that is shorter than the default login failure window (10 minutes), so a user
// who waits is not kept out longer than our own limiter would, and a client that
// retries too early simply receives the same response again.
const providerLockoutRetryAfter = 5 * time.Minute

// handleProviderLockout answers a Firebase account lockout with the same
// LoginRateLimited response used by the local limiter (429, generic message,
// challengeRequired=true, Retry-After), so it reveals nothing beyond what the
// earlier credential rejections and the existing rate limit already do. The
// attempt is recorded exactly once, as a denied attempt: Firebase did not check
// the password, but the request still counts toward the IP and tenant limits.
func (service *IAMCommandService) handleProviderLockout(ctx context.Context, signals types.LoginAbuseSignals) error {
	log.Printf("[security] event=login_provider_lockout tenant_id=%s", signals.TenantID)
	retryAfter := providerLockoutRetryAfter
	if service.LoginAbuseProtectionInterface != nil {
		decision, err := service.LoginAbuseProtectionInterface.RecordLoginFailure(ctx, signals)
		if err != nil {
			log.Printf("[security] event=login_protection_unavailable operation=record reason=provider_lockout")
		} else if decision.RetryAfter > retryAfter {
			retryAfter = decision.RetryAfter
		}
	}
	return newLoginRateLimitError(retryAfter)
}

func (service *IAMCommandService) handleDeniedLogin(ctx context.Context, signals types.LoginAbuseSignals, originalCode string) error {
	if service.LoginAbuseProtectionInterface == nil {
		return newLoginError(apiError.LoginProtectionUnavailable, true, 0)
	}
	decision, err := service.LoginAbuseProtectionInterface.RecordLoginFailure(ctx, signals)
	if err != nil {
		return newLoginError(apiError.LoginProtectionUnavailable, true, 0)
	}
	if decision.RetryAfter > 0 {
		return newLoginRateLimitError(decision.RetryAfter)
	}
	if service.loginCooldownMode() {
		return newLoginRateLimitError(loginCooldownRetryAfter(decision))
	}
	return newLoginError(originalCode, true, 0)
}

func (service *IAMCommandService) loginChallengeMode() LoginChallengeMode {
	if service.LoginChallengeMode == LoginChallengeModeCooldown {
		return LoginChallengeModeCooldown
	}
	return LoginChallengeModeTurnstile
}

func (service *IAMCommandService) loginCooldownMode() bool {
	return service.loginChallengeMode() == LoginChallengeModeCooldown
}

// loginCooldownRetryAfter is the wait advertised in cooldown mode. The
// protection layer already bounds it; the floor only guards a decision that
// carries no TTL (for example from a protection implementation that does not
// report one).
func loginCooldownRetryAfter(decision types.LoginProtectionDecision) time.Duration {
	if decision.ChallengeRetryAfter < minimumLoginCooldown {
		return minimumLoginCooldown
	}
	return decision.ChallengeRetryAfter
}

func (service *IAMCommandService) validateLoginTurnstile(ctx context.Context, token string, signals types.LoginAbuseSignals) error {
	if service.LoginTurnstileAPIInterface == nil || len(service.LoginTurnstileAllowedHostnames) == 0 {
		log.Printf("[security] event=login_turnstile_unavailable reason=not_configured")
		return newLoginError(apiError.CloudflareAPIError, true, 0)
	}
	response, err := service.LoginTurnstileAPIInterface.ValidateTurnstileTokenWithRemoteIP(ctx, token, signals.ClientIP)
	if err != nil {
		log.Printf("[security] event=login_turnstile_unavailable reason=provider")
		return newLoginError(apiError.CloudflareAPIError, true, 0)
	}
	if !response.Success {
		if isLoginTurnstileProviderFailure(response.ErrorCodes) {
			log.Printf("[security] event=login_turnstile_unavailable reason=provider_response")
			return newLoginError(apiError.CloudflareAPIError, true, 0)
		}
		log.Printf("[security] event=login_turnstile_rejected reason=invalid")
		return service.handleDeniedLogin(ctx, signals, apiError.TurnstileInvalid)
	}
	if response.Action != "login" {
		log.Printf("[security] event=login_turnstile_rejected reason=action_mismatch")
		return service.handleDeniedLogin(ctx, signals, apiError.TurnstileInvalid)
	}
	hostname := strings.ToLower(strings.TrimSpace(response.Hostname))
	if _, allowed := service.LoginTurnstileAllowedHostnames[hostname]; !allowed {
		log.Printf("[security] event=login_turnstile_rejected reason=hostname_mismatch")
		return service.handleDeniedLogin(ctx, signals, apiError.TurnstileInvalid)
	}
	return nil
}

func isLoginTurnstileProviderFailure(errorCodes []string) bool {
	for _, errorCode := range errorCodes {
		switch errorCode {
		case "missing-input-secret", "invalid-input-secret", "bad-request", "internal-error":
			return true
		}
	}
	return false
}

func (service *IAMCommandService) resetLoginAccountFailures(ctx context.Context, signals types.LoginAbuseSignals) error {
	if service.LoginAbuseProtectionInterface == nil {
		return errors.New(apiError.LoginProtectionUnavailable)
	}
	return service.LoginAbuseProtectionInterface.ResetAccountFailures(ctx, signals)
}

func newLoginError(code string, challengeRequired bool, retryAfterSeconds int) error {
	return &apiError.LoginError{
		Code:              code,
		ChallengeRequired: challengeRequired,
		RetryAfterSeconds: retryAfterSeconds,
	}
}

func newLoginRateLimitError(retryAfter time.Duration) error {
	retryAfterSeconds := int(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		retryAfterSeconds++
	}
	return newLoginError(apiError.LoginRateLimited, true, retryAfterSeconds)
}

func LoginTurnstileAllowedHostnamesFromEnvironment(value string) map[string]struct{} {
	hostnames := make(map[string]struct{})
	for _, hostname := range strings.Split(value, ",") {
		hostname = strings.ToLower(strings.TrimSpace(hostname))
		if hostname != "" {
			hostnames[hostname] = struct{}{}
		}
	}
	return hostnames
}

func (service *IAMCommandService) logSuccessfulLogin(ctx context.Context, sessionToken, tenantID string, user userServiceTypes.GetTenantUser) {
	if service.TenantQueryServiceInterface == nil || service.ElasticsearchCommandServiceInterface == nil {
		return
	}
	auditContext := context.WithoutCancel(ctx)
	go func() {
		auditContext, cancel := context.WithTimeout(auditContext, 10*time.Second)
		defer cancel()

		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(auditContext, tenantID)
		if err != nil {
			return
		}
		_, err = service.ElasticsearchCommandServiceInterface.CreateLoginLog(auditContext, elasticsearchTypes.CreateLoginLog{
			SessionID:  sessionToken,
			TenantID:   tenantID,
			TenantName: tenant.Name,
			UserID:     user.ID,
			Email:      user.Email,
			Name:       user.Name,
			Role:       user.Role,
			Specialty:  user.Specialty,
		})
		if err != nil {
			log.Println(err)
		}
	}()
}

func (service *IAMCommandService) SetUserSuspended(_ context.Context, tenantID, userID string) (bool, error) {
	return service.IAMCommandRepositoryInterface.SetUserSuspended(tenantID, userID)
}

func (service *IAMCommandService) AcquireUserAccessTransition(_ context.Context, tenantID, userID, ownerToken string, ttl time.Duration) (bool, error) {
	return service.IAMCommandRepositoryInterface.AcquireUserAccessTransition(tenantID, userID, ownerToken, ttl)
}

func (service *IAMCommandService) ReleaseUserAccessTransition(_ context.Context, tenantID, userID, ownerToken string) error {
	return service.IAMCommandRepositoryInterface.ReleaseUserAccessTransition(tenantID, userID, ownerToken)
}

func (service *IAMCommandService) ClearUserSuspension(_ context.Context, tenantID, userID string) error {
	return service.IAMCommandRepositoryInterface.ClearUserSuspension(tenantID, userID)
}

func (service *IAMCommandService) RevokeUserSessions(_ context.Context, tenantID, userID string) error {
	return service.IAMCommandRepositoryInterface.RevokeUserSessions(tenantID, userID)
}

// SetTokenSession sets token session
func (service *IAMCommandService) SetTokenSession(ctx context.Context, data types.SetTokenSession) error {
	err := service.IAMCommandRepositoryInterface.SetTokenSession(repositoryTypes.SetTokenSession{
		SessionID:           data.SessionID,
		TenantID:            data.TenantID,
		UserID:              data.UserID,
		Role:                data.Role,
		ExpireTimeInSeconds: entity.ExpireTimeInSeconds,
	})
	if err != nil {
		return err
	}

	return nil
}

// VerifyTenantUserEmail emails a verification link to an existing account.
// The response is the same whether or not the account exists.
func (service *IAMCommandService) VerifyTenantUserEmail(ctx context.Context, tenantID, email, clientIP string) error {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	return service.handlePublicEmailRequest(ctx, "email_verification", tenantID, normalizedEmail, clientIP, service.sendVerificationEmail)
}

func (service *IAMCommandService) sendVerificationEmail(ctx context.Context, tenantID, email string) {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return
	}

	verifyLink, err := tenantAuth.EmailVerificationLink(ctx, email)
	if err != nil {
		log.Println(err)
		return
	}

	textMessage := fmt.Sprintf("Hello,<br /><br />"+
		"Follow this link to verify your PACS AI email address for your %s account:<br /><br />"+
		"<a href=\"%s\">%s</a><br /><br />"+
		"If you did not create a PACS AI account, you can ignore this email.<br /><br />"+
		"Thanks,<br />"+
		"Your PACS AI team", email, verifyLink, verifyLink)
	err = service.MailgunSDK.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Verify your email",
		Recipient:     email,
		PlainTextBody: textMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification email via mailgun", err)
	}
}

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
