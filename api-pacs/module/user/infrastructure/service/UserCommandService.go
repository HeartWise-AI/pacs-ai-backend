package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/segmentio/ksuid"

	cloudflareAPITypes "api-pacs/infrastructures/providers/api/cloudflare/types"
	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	"api-pacs/module/elasticsearch/domain/entity"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	inferenceApplication "api-pacs/module/inference/application"
	inferenceTypes "api-pacs/module/inference/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	userEntity "api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserCommandService handles the User command service logic
type UserCommandService struct {
	cloudflareAPITypes.CloudflareAPIInterface
	userApplication.RegistrationRateLimiterInterface
	repository.UserCommandRepositoryInterface
	repository.UserQueryRepositoryInterface
	tenantApplication.TenantCommandServiceInterface
	tenantApplication.TenantQueryServiceInterface
	inferenceApplication.InferenceCommandServiceInterface
	inferenceApplication.InferenceQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	iamApplication.IAMCommandServiceInterface
	mailgunTypes.MailgunSDKInterface
	PolicyCatalog *PolicyCatalog
}

const (
	userInviteTemplate                   string        = "%s/register?t=%s&email=%s&code=%s"
	registrationVerificationEmailTimeout time.Duration = time.Minute
	registrationPolicyRollbackTimeout    time.Duration = 10 * time.Second
	userAccessTransitionLockTTL          time.Duration = 2 * time.Minute
	userAccessTransitionOperationTimeout time.Duration = 90 * time.Second
	userAccessTransitionReleaseTimeout   time.Duration = 10 * time.Second
)

// AcceptPolicies records the exact current required policy versions for an
// authenticated user. Repeating the same request is idempotent in persistence.
func (service *UserCommandService) AcceptPolicies(ctx context.Context, data types.AcceptPolicies) error {
	if strings.TrimSpace(data.TenantID) == "" || strings.TrimSpace(data.UserID) == "" ||
		(data.Source != userEntity.PolicyAcceptanceSourceRegistration && data.Source != userEntity.PolicyAcceptanceSourceAuthenticated) {
		return errors.New(apiError.InvalidPayload)
	}
	current, err := service.PolicyCatalog.ValidateAcceptances(data.TenantID, data.Acceptances)
	if err != nil {
		return err
	}

	acceptedAt := time.Now().Unix()
	acceptances := make([]userEntity.UserPolicyAcceptance, 0, len(current))
	for _, policy := range current {
		if !policy.Required {
			continue
		}
		acceptances = append(acceptances, userEntity.UserPolicyAcceptance{
			TenantID: data.TenantID, UserID: data.UserID, PolicyKey: policy.PolicyKey,
			Version: policy.Version, AcceptedAt: acceptedAt, Source: data.Source,
		})
	}
	if err := service.UserCommandRepositoryInterface.InsertUserPolicyAcceptances(ctx, acceptances); err != nil {
		return err
	}
	log.Printf("[audit] event=policy_acceptance tenant_id=%s user_id=%s policy_count=%d source=%s", data.TenantID, data.UserID, len(acceptances), data.Source)
	return nil
}

// ChangeTenantUserAccess applies tenant-scoped role hierarchy and synchronizes
// durable account state with immediate Redis session enforcement.
func (service *UserCommandService) ChangeTenantUserAccess(ctx context.Context, data types.ChangeTenantUserAccess) error {
	if data.AccessState != userEntity.AccountAccessActive && data.AccessState != userEntity.AccountAccessSuspended {
		return errors.New(apiError.InvalidPayload)
	}
	lockToken, err := service.acquireUserAccessTransition(ctx, data.TenantID, data.TargetUserID)
	if err != nil {
		return err
	}
	defer service.releaseUserAccessTransition(ctx, data.TenantID, data.TargetUserID, lockToken)
	transitionContext, cancel := context.WithTimeout(ctx, userAccessTransitionOperationTimeout)
	defer cancel()

	target, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(transitionContext, data.TenantID, data.TargetUserID)
	if err != nil {
		return err
	}
	actor, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(transitionContext, data.TenantID, data.ActorUserID)
	if err != nil {
		return err
	}
	data.ActorRole = actor.Role
	if userEntity.ResolveAccountAccessState(actor.AccessState, actor.IsAccountDisabled) != userEntity.AccountAccessActive {
		err := errors.New(apiError.ForbiddenAccess)
		service.logAccountAccessAudit(transitionContext, data, target, "REJECTED", err.Error())
		return err
	}
	if err := authorizeAccountManagement(data.ActorUserID, data.ActorRole, target.ID, target.Role); err != nil {
		service.logAccountAccessAudit(transitionContext, data, target, "REJECTED", err.Error())
		return err
	}

	previousState := userEntity.ResolveAccountAccessState(target.AccessState, target.IsAccountDisabled)
	if data.AccessState == userEntity.AccountAccessSuspended {
		markerCreated, err := service.IAMCommandServiceInterface.SetUserSuspended(transitionContext, data.TenantID, target.ID)
		if err != nil {
			service.logAccountAccessAudit(transitionContext, data, target, "REJECTED", err.Error())
			return err
		}
		rollbackMarker := markerCreated && previousState != userEntity.AccountAccessSuspended
		if err := service.IAMCommandServiceInterface.RevokeUserSessions(transitionContext, data.TenantID, target.ID); err != nil {
			service.clearSuspensionMarkerAfterFailure(transitionContext, data.TenantID, target.ID, rollbackMarker)
			service.logAccountAccessAudit(transitionContext, data, target, "REJECTED", err.Error())
			return err
		}
		if previousState != data.AccessState {
			if err := service.UserCommandRepositoryInterface.UpdateTenantUserAccessState(transitionContext, repositoryTypes.UpdateTenantUserAccessState{
				ID: target.ID, TenantID: data.TenantID, AccessState: data.AccessState,
			}); err != nil {
				service.clearSuspensionMarkerAfterFailure(transitionContext, data.TenantID, target.ID, rollbackMarker)
				service.logAccountAccessAudit(transitionContext, data, target, "REJECTED", err.Error())
				return err
			}
		}
	} else {
		if previousState != data.AccessState {
			if err := service.UserCommandRepositoryInterface.UpdateTenantUserAccessState(transitionContext, repositoryTypes.UpdateTenantUserAccessState{
				ID: target.ID, TenantID: data.TenantID, AccessState: data.AccessState,
			}); err != nil {
				service.logAccountAccessAudit(transitionContext, data, target, "REJECTED", err.Error())
				return err
			}
		}
		if err := service.IAMCommandServiceInterface.ClearUserSuspension(transitionContext, data.TenantID, target.ID); err != nil {
			service.logAccountAccessAudit(transitionContext, data, target, "REJECTED", err.Error())
			return err
		}
	}

	service.logAccountAccessAudit(transitionContext, data, target, "SUCCESS", "")
	return nil
}

func (service *UserCommandService) acquireUserAccessTransition(ctx context.Context, tenantID, userID string) (string, error) {
	lockToken := ksuid.New().String()
	acquired, err := service.IAMCommandServiceInterface.AcquireUserAccessTransition(
		ctx, tenantID, userID, lockToken, userAccessTransitionLockTTL,
	)
	if err != nil {
		return "", err
	}
	if !acquired {
		return "", errors.New(apiError.AccountAccessTransitionInProgress)
	}
	return lockToken, nil
}

func (service *UserCommandService) releaseUserAccessTransition(ctx context.Context, tenantID, userID, lockToken string) {
	releaseContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), userAccessTransitionReleaseTimeout)
	defer cancel()
	if err := service.IAMCommandServiceInterface.ReleaseUserAccessTransition(releaseContext, tenantID, userID, lockToken); err != nil {
		log.Printf("[security] severity=critical event=account_access_transition_lock_release_failed tenant_id=%s user_id=%s error=%v", tenantID, userID, err)
	}
}

func (service *UserCommandService) clearSuspensionMarkerAfterFailure(ctx context.Context, tenantID, userID string, markerCreated bool) {
	if !markerCreated {
		return
	}
	rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := service.IAMCommandServiceInterface.ClearUserSuspension(rollbackContext, tenantID, userID); err != nil {
		log.Printf("[security] severity=critical event=account_suspension_marker_rollback_failed tenant_id=%s user_id=%s error=%v", tenantID, userID, err)
	}
}

func authorizeAccountManagement(actorID, actorRole, targetID, targetRole string) error {
	if actorID == targetID {
		return errors.New(apiError.ForbiddenAccess)
	}
	rank := map[string]int{iamEntity.UserRole: 1, iamEntity.AdminRole: 2, iamEntity.OwnerRole: 3}
	actorRank, actorRoleKnown := rank[actorRole]
	targetRank, targetRoleKnown := rank[targetRole]
	if !actorRoleKnown || !targetRoleKnown || actorRank < rank[iamEntity.AdminRole] || actorRank <= targetRank {
		return errors.New(apiError.ForbiddenAccess)
	}
	return nil
}

func (service *UserCommandService) logAccountAccessAudit(ctx context.Context, data types.ChangeTenantUserAccess, target repositoryTypes.GetTenantUser, outcome, failureReason string) {
	previousState := userEntity.ResolveAccountAccessState(target.AccessState, target.IsAccountDisabled)
	log.Printf(
		"[security] event=account_access_transition tenant_id=%s actor_user_id=%s actor_role=%s target_user_id=%s target_role=%s previous_state=%s new_state=%s outcome=%s reason_present=%t failure_reason=%s",
		data.TenantID,
		data.ActorUserID,
		data.ActorRole,
		target.ID,
		target.Role,
		previousState,
		data.AccessState,
		outcome,
		data.Reason != "",
		failureReason,
	)

	if service.ElasticsearchCommandServiceInterface == nil || service.TenantQueryServiceInterface == nil {
		return
	}
	auditContext := context.WithoutCancel(ctx)
	go func() {
		auditContext, cancel := context.WithTimeout(auditContext, 10*time.Second)
		defer cancel()
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(auditContext, data.TenantID)
		if err != nil {
			log.Printf("[security] event=account_access_audit_failed tenant_id=%s target_user_id=%s error=%v", data.TenantID, target.ID, err)
			return
		}
		action := entity.ReactivateAction
		if data.AccessState == userEntity.AccountAccessSuspended {
			action = entity.SuspendAction
		}
		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(auditContext, elasticsearchTypes.CreateAdminMemberLog{
			TenantID: data.TenantID, TenantName: tenant.Name, UserID: target.ID,
			Email: target.Email, Name: target.Name, Role: target.Role, LicenseNo: target.LicenseNo, Specialty: target.Specialty,
			Action: action, ActorUserID: data.ActorUserID, ActorRole: data.ActorRole,
			PreviousState: previousState, NewState: data.AccessState, Reason: data.Reason,
			Outcome: outcome, FailureReason: failureReason,
		})
		if err != nil {
			log.Printf("[security] event=account_access_audit_failed tenant_id=%s target_user_id=%s error=%v", data.TenantID, target.ID, err)
		}
	}()
}

// CreateTenantUser add a new tenant user with random generated password
func (service *UserCommandService) CreateTenantUser(ctx context.Context, data types.CreateTenantUser) (string, error) {
	// generate random password
	generatedPassword := generateID()

	uid, err := service.UserCommandRepositoryInterface.InsertTenantUser(ctx, repositoryTypes.CreateTenantUser{
		TenantID:       data.TenantID,
		Role:           data.Role,
		Name:           data.Name,
		Email:          data.Email,
		Password:       generatedPassword,
		LicenseNo:      data.LicenseNo,
		Specialty:      data.Specialty,
		IsAdminCreated: true, // admin created user
	})
	if err != nil {
		return "", err
	}

	// logs to elasticsearch
	go func() {
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   data.TenantID,
			TenantName: tenant.Name,
			UserID:     uid,
			Email:      data.Email,
			Name:       data.Name,
			Role:       data.Role,
			LicenseNo:  data.LicenseNo,
			Specialty:  data.Specialty,
			Action:     entity.CreateAction,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	// log to elasticsearch
	go func() {
		//  redirect link
		redirectLink := fmt.Sprintf("%s/login?t=%s", os.Getenv("APP_URL"), data.TenantID)

		// send to email
		emailMessage := fmt.Sprintf("Hi %s, <br /><br />"+
			"Here is your new PACS AI account credentials:<br /><br />"+
			"Email: %s <br />"+
			"Password: %s <br /><br />"+
			"You can use this and login to PACS AI via <a href=\"%s\">%s</a>. You will be then prompted to change password. <br /><br />"+
			"Thanks, <br /><br />"+
			"Your PACS AI team", data.Name, data.Email, generatedPassword, redirectLink, redirectLink)
		err = service.MailgunSDKInterface.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
			Subject:       "[PACS AI]: New account credentials",
			Recipient:     data.Email,
			PlainTextBody: emailMessage,
		})
		if err != nil {
			log.Println("[error] cannot send account credentials via aws ses", err)
			return
		}
	}()

	return generatedPassword, nil
}

// DeleteTenantUser delete tenant user by id
func (service *UserCommandService) DeleteTenantUser(ctx context.Context, data types.DeleteTenantUser) error {
	lockToken, err := service.acquireUserAccessTransition(ctx, data.TenantID, data.TargetUserID)
	if err != nil {
		return err
	}
	defer service.releaseUserAccessTransition(ctx, data.TenantID, data.TargetUserID, lockToken)
	transitionContext, cancel := context.WithTimeout(ctx, userAccessTransitionOperationTimeout)
	defer cancel()

	user, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(transitionContext, data.TenantID, data.TargetUserID)
	if err != nil {
		return err
	}
	actor, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(transitionContext, data.TenantID, data.ActorUserID)
	if err != nil {
		return err
	}
	data.ActorRole = actor.Role
	if userEntity.ResolveAccountAccessState(actor.AccessState, actor.IsAccountDisabled) != userEntity.AccountAccessActive {
		return errors.New(apiError.ForbiddenAccess)
	}
	if err := authorizeAccountManagement(data.ActorUserID, data.ActorRole, user.ID, user.Role); err != nil {
		return err
	}
	previousState := userEntity.ResolveAccountAccessState(user.AccessState, user.IsAccountDisabled)
	markerCreated, err := service.IAMCommandServiceInterface.SetUserSuspended(transitionContext, data.TenantID, user.ID)
	if err != nil {
		return err
	}
	rollbackMarker := markerCreated && previousState != userEntity.AccountAccessSuspended
	if err := service.IAMCommandServiceInterface.RevokeUserSessions(transitionContext, data.TenantID, user.ID); err != nil {
		service.clearSuspensionMarkerAfterFailure(transitionContext, data.TenantID, user.ID, rollbackMarker)
		return err
	}

	err = service.UserCommandRepositoryInterface.DeleteTenantUser(transitionContext, data.TenantID, user.ID)
	if err != nil {
		service.clearSuspensionMarkerAfterFailure(transitionContext, data.TenantID, user.ID, rollbackMarker)
		log.Println(err)
		return err
	}

	// log to elasticsearch without inheriting request cancellation
	if service.TenantQueryServiceInterface != nil && service.ElasticsearchCommandServiceInterface != nil {
		auditContext := context.WithoutCancel(ctx)
		go func() {
			auditContext, cancel := context.WithTimeout(auditContext, 10*time.Second)
			defer cancel()
			tenant, err := service.TenantQueryServiceInterface.GetTenantByID(auditContext, data.TenantID)
			if err != nil {
				return
			}

			_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(auditContext, elasticsearchTypes.CreateAdminMemberLog{
				TenantID:      user.TenantID,
				TenantName:    tenant.Name,
				UserID:        user.ID,
				Email:         user.Email,
				Name:          user.Name,
				Role:          user.Role,
				LicenseNo:     user.LicenseNo,
				Specialty:     user.Specialty,
				Action:        entity.DeleteAction,
				ActorUserID:   data.ActorUserID,
				ActorRole:     data.ActorRole,
				PreviousState: userEntity.ResolveAccountAccessState(user.AccessState, user.IsAccountDisabled),
				NewState:      "DELETED",
				Reason:        data.Reason,
				Outcome:       "SUCCESS",
			})
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

	return nil
}

// DeleteTenantUserEmailInvite delete tenant user email invite by id
func (service *UserCommandService) DeleteTenantUserEmailInvite(ctx context.Context, ID string) error {
	err := service.UserCommandRepositoryInterface.DeleteTenantUserEmailInvite(ctx, ID)
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// RegisterTenantUser registers a tenant user
func (service *UserCommandService) RegisterTenantUser(ctx context.Context, data types.RegisterTenantUser) error {
	if service.RegistrationRateLimiterInterface == nil {
		log.Printf("[security] event=registration_rate_limit_unavailable tenant_id=%s", data.TenantID)
		return errors.New(apiError.DatabaseError)
	}

	retryAfter, err := service.RegistrationRateLimiterInterface.CheckRegistrationAttempt(ctx, types.RegistrationRateLimit{
		TenantID: data.TenantID,
		Email:    data.Email,
		ClientIP: data.ClientIP,
	})
	if err != nil {
		log.Printf("[security] event=registration_rate_limit_unavailable tenant_id=%s", data.TenantID)
		return errors.New(apiError.DatabaseError)
	}
	if retryAfter > 0 {
		retryAfterSeconds := int((retryAfter + time.Second - 1) / time.Second)
		log.Printf("[security] event=registration_rate_limited tenant_id=%s", data.TenantID)
		return &apiError.RegistrationRateLimitError{RetryAfterSeconds: retryAfterSeconds}
	}

	turnstileResponse, err := service.CloudflareAPIInterface.ValidateTurnstileToken(ctx, data.TurnstileToken)
	if err != nil {
		log.Printf("[security] event=registration_turnstile_unavailable tenant_id=%s", data.TenantID)
		return errors.New(apiError.CloudflareAPIError)
	}
	if !turnstileResponse.Success {
		if isTurnstileProviderFailure(turnstileResponse.ErrorCodes) {
			log.Printf("[security] event=registration_turnstile_unavailable tenant_id=%s", data.TenantID)
			return errors.New(apiError.CloudflareAPIError)
		}
		log.Printf("[security] event=registration_turnstile_rejected tenant_id=%s", data.TenantID)
		return errors.New(apiError.TurnstileInvalid)
	}

	// get tenant
	tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
	if err != nil {
		return err
	}

	// check if registration is enabled
	if !tenant.OnboardingEnableRegistration {
		return errors.New(apiError.ForbiddenAccess)
	}

	// Validate the exact deployment-owned versions before creating any identity.
	if _, err := service.PolicyCatalog.ValidateAcceptances(data.TenantID, data.PolicyAcceptances); err != nil {
		return err
	}

	// check if email already exists
	_, err = service.UserQueryRepositoryInterface.SelectTenantUserByEmail(ctx, data.TenantID, data.Email)
	if err == nil {
		return errors.New(apiError.DuplicateRecord)
	} else if err.Error() != apiError.MissingRecord {
		return err
	}

	var isEmailVerified bool

	// check if code is provided - from invite validate code and expiration
	if data.Code != nil {
		// get tenant email invite by email
		emailInvite, err := service.UserQueryRepositoryInterface.SelectTenantUserEmailInviteByEmail(ctx, data.TenantID, data.Email)
		if err != nil {
			return errors.New(apiError.UnauthorizedAccess)
		}

		// check expiration
		if time.Now().Unix() > int64(emailInvite.ExpiresAt) {
			return errors.New(apiError.UnauthorizedAccess)
		}

		// validate code
		if emailInvite.Code != *data.Code {
			return errors.New(apiError.UnauthorizedAccess)
		}

		// update tenant user invite verified at
		err = service.UserCommandRepositoryInterface.UpdateTenantUserEmailInviteVerifiedAt(ctx, emailInvite.ID)
		if err != nil {
			return err
		}

		// set email verified to true
		isEmailVerified = true
	}

	// insert tenant user
	userID, err := service.UserCommandRepositoryInterface.InsertTenantUser(ctx, repositoryTypes.CreateTenantUser{
		TenantID:        data.TenantID,
		Role:            iamEntity.UserRole,
		Email:           data.Email,
		Name:            data.Name,
		Password:        data.Password,
		LicenseNo:       data.LicenseNo,
		Specialty:       data.Specialty,
		IsEmailVerified: isEmailVerified,
	})
	if err != nil {
		return err
	}
	if err := service.AcceptPolicies(ctx, types.AcceptPolicies{
		TenantID: data.TenantID, UserID: userID, Source: userEntity.PolicyAcceptanceSourceRegistration,
		Acceptances: data.PolicyAcceptances,
	}); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), registrationPolicyRollbackTimeout)
		defer cancel()
		if cleanupErr := service.UserCommandRepositoryInterface.DeleteTenantUser(cleanupCtx, data.TenantID, userID); cleanupErr != nil {
			log.Printf("[security] severity=critical event=registration_policy_rollback_failed tenant_id=%s user_id=%s error=%v", data.TenantID, userID, cleanupErr)
		} else {
			log.Printf("[security] event=registration_policy_rollback_completed tenant_id=%s user_id=%s", data.TenantID, userID)
		}
		return err
	}
	if !isEmailVerified {
		// The verification email runs after the HTTP handler returns, so it must not
		// inherit request cancellation. Keep it time-bounded to avoid leaking work.
		emailContext := context.WithoutCancel(ctx)
		go func() {
			emailContext, cancel := context.WithTimeout(emailContext, registrationVerificationEmailTimeout)
			defer cancel()

			if err := service.SendTenantUserEmailVerification(emailContext, data.TenantID, data.Email); err != nil {
				log.Println("[error] cannot send verification email after registration", err)
			}
		}()
	}

	return nil
}

func isTurnstileProviderFailure(errorCodes []string) bool {
	for _, errorCode := range errorCodes {
		switch errorCode {
		case "missing-input-secret", "invalid-input-secret", "bad-request", "internal-error":
			return true
		}
	}

	return false
}

// SendTenantUserEmailVerification sends a Firebase email verification link to a tenant user.
func (service *UserCommandService) SendTenantUserEmailVerification(ctx context.Context, tenantID, email string) error {
	verifyLink, err := service.UserCommandRepositoryInterface.GenerateTenantUserEmailVerificationLink(ctx, tenantID, email)
	if err != nil {
		return err
	}

	emailMessage := fmt.Sprintf("Hello,<br /><br />"+
		"Follow this link to verify your PACS AI email address for your %s account:<br /><br />"+
		"<a href=\"%s\">%s</a><br /><br />"+
		"If you did not create a PACS AI account, you can ignore this email.<br /><br />"+
		"Thanks,<br />"+
		"Your PACS AI team", email, verifyLink, verifyLink)

	err = service.MailgunSDKInterface.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Verify your email",
		Recipient:     email,
		PlainTextBody: emailMessage,
	})
	if err != nil {
		return errors.New(apiError.MailgunError)
	}

	return nil
}

// ResetTutorial resets the tutorial for a user
func (service *UserCommandService) ResetTutorial(ctx context.Context, data types.ResetTutorial) error {
	// get onboarding questionnaire answers
	onboardingQuestionnaireAnswers, err := service.TenantQueryServiceInterface.GetOnboardingQuestionnaireAnswers(ctx, tenantTypes.GetOnboardingQuestionnaireAnswer{
		TenantID: data.TenantID,
		UserID:   data.UserID,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// remove onboarding questionnaire answers
	for _, onboardingQuestionnaireAnswer := range onboardingQuestionnaireAnswers {
		err = service.TenantCommandServiceInterface.RemoveOnboardingQuestionnaireAnswer(ctx, onboardingQuestionnaireAnswer.ID)
		if err != nil {
			return err
		}
	}

	// get onboarding model questionnaire answers
	onboardingModelQuestionnaireAnswers, err := service.InferenceQueryServiceInterface.GetOnboardingModelQuestionnaireAnswers(ctx, inferenceTypes.GetOnboardingModelQuestionnaireAnswer{
		TenantID: data.TenantID,
		UserID:   data.UserID,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// remove onboarding model questionnaire answers
	for _, onboardingModelQuestionnaireAnswer := range onboardingModelQuestionnaireAnswers {
		err = service.InferenceCommandServiceInterface.RemoveOnboardingModelQuestionnaireAnswer(ctx, onboardingModelQuestionnaireAnswer.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// ResendTenantUserEmailInvite resends a tenant user email invite to the email
func (service *UserCommandService) ResendTenantUserEmailInvite(ctx context.Context, data types.ResendTenantUserEmailInvite) error {
	// get tenant user email invite by id
	userInviteByID, err := service.UserQueryRepositoryInterface.SelectTenantUserEmailInviteByID(ctx, data.TenantID, data.ID)
	if err != nil {
		return err
	}

	// generate code
	code := generateID()

	// update tenant user invite code and expiration
	err = service.UserCommandRepositoryInterface.UpdateTenantUserEmailInvite(ctx, repositoryTypes.UpdateTenantUserEmailInvite{
		ID:        userInviteByID.ID,
		Code:      code,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days expiration
	})
	if err != nil {
		return err
	}

	// send to email
	redirectURL := fmt.Sprintf(userInviteTemplate, os.Getenv("APP_URL"), data.TenantID, userInviteByID.Email, code)

	emailMessage := fmt.Sprintf("Hi %s, <br /><br />"+
		"You have been invited to join PACS AI. Please click the link below to accept the invitation: <br /><br />"+
		"<a href=\"%s\">%s</a> <br /><br />"+
		"Your PACS AI Team", userInviteByID.Email, redirectURL, redirectURL)

	err = service.MailgunSDKInterface.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Invitation to join workspace",
		Recipient:     userInviteByID.Email,
		PlainTextBody: emailMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification code via mailgun", err)
		return errors.New(apiError.MailgunError)
	}

	return nil
}

// SendTenantUserEmailInvite sends a tenant user email invite to the email
func (service *UserCommandService) SendTenantUserEmailInvite(ctx context.Context, data types.SendTenantUserEmailInvite) error {
	// check if email invite already exists
	_, err := service.UserQueryRepositoryInterface.SelectTenantUserEmailInviteByEmail(ctx, data.TenantID, data.Email)
	if err == nil {
		return errors.New(apiError.DuplicateRecord)
	} else if err.Error() != apiError.MissingRecord {
		return err
	}

	// check if email already exists in users
	_, err = service.UserQueryRepositoryInterface.SelectTenantUserByEmail(ctx, data.TenantID, data.Email)
	if err == nil {
		return errors.New(apiError.DuplicateRecord)
	} else if err.Error() != apiError.MissingRecord {
		return err
	}

	// generate code
	code := generateID()

	// insert tenant user invite
	err = service.UserCommandRepositoryInterface.InsertTenantUserEmailInvite(ctx, repositoryTypes.CreateTenantUserEmailInvite{
		ID:        generateID(),
		TenantID:  data.TenantID,
		Code:      code,
		Email:     data.Email,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days expiration
	})
	if err != nil {
		return err
	}

	// log to elasticsearch
	go func() {
		// get tenant
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminInviteLog(ctx, elasticsearchTypes.CreateAdminInviteLog{
			TenantID:   data.TenantID,
			TenantName: tenant.Name,
			Email:      data.Email,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	// send to email
	go func() {
		redirectURL := fmt.Sprintf(userInviteTemplate, os.Getenv("APP_URL"), data.TenantID, data.Email, code)

		emailMessage := fmt.Sprintf("Hi %s, <br /><br />"+
			"You have been invited to join PACS AI. Please click the link below to accept the invitation: <br /><br />"+
			"<a href=\"%s\">%s</a> <br /><br />"+
			"Your PACS AI Team", data.Email, redirectURL, redirectURL)

		err = service.MailgunSDKInterface.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
			Subject:       "[PACS AI]: Invitation to join workspace",
			Recipient:     data.Email,
			PlainTextBody: emailMessage,
		})
		if err != nil {
			log.Println("[error] cannot send verification code via mailgun", err)
			return
		}
	}()

	return nil
}

// UpdateTenantUser update tenant user
func (service *UserCommandService) UpdateTenantUser(ctx context.Context, data types.UpdateTenantUser) error {
	err := service.UserCommandRepositoryInterface.UpdateTenantUser(ctx, repositoryTypes.UpdateTenantUser{
		ID:        data.ID,
		TenantID:  data.TenantID,
		Role:      data.Role,
		Name:      data.Name,
		LicenseNo: data.LicenseNo,
		Specialty: data.Specialty,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	user, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(ctx, data.TenantID, data.ID)
	if err != nil {
		return err
	}

	// log to elasticsearch
	go func() {
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   user.TenantID,
			TenantName: tenant.Name,
			UserID:     user.ID,
			Email:      user.Email,
			Name:       user.Name,
			Role:       user.Role,
			LicenseNo:  user.LicenseNo,
			Specialty:  user.Specialty,
			Action:     entity.UpdateAction,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	return nil
}

// UpdateTenantUserPassword update user password
func (service *UserCommandService) UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error {
	err := service.UserCommandRepositoryInterface.UpdateTenantUserPassword(ctx, repositoryTypes.UpdateTenantUserPassword{
		ID:          data.ID,
		TenantID:    data.TenantID,
		NewPassword: data.NewPassword,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// UpdateUserMetadata update user metadata
func (service *UserCommandService) UpdateUserMetadata(ctx context.Context, data types.UpdateUserMetadata) error {
	metadataBytes, err := json.Marshal(data.Metadata)
	if err != nil {
		return err
	}

	err = service.UserCommandRepositoryInterface.UpsertUserMetadata(ctx, repositoryTypes.UpsertUserMetadata{
		UserID:   data.UserID,
		Metadata: string(metadataBytes),
	})
	if err != nil {
		return err
	}

	return nil
}

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
