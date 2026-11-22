package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/domain/entity"
	iamRepository "api-pacs/module/iam/infrastructure/repository"
	"api-pacs/module/iam/infrastructure/service/types"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

// Sign-in handoff (CA-268): another app (CardioAgent) opens PACS-AI already
// signed in, for users who linked their two accounts.
//
//   - Mint (internal, service token): the app names its verified user. If that
//     user is linked to a PACS-AI user, a one-time "session" code is returned;
//     otherwise a one-time "link" code that the user redeems after signing in to
//     PACS-AI with their own password. Email never decides who is signed in.
//   - Exchange (public): a session code becomes a normal PACS-AI session.
//   - Link (signed in): a link code binds the app's user to the signed-in user.
//
// Codes are 32 random bytes, kept in Redis only as their SHA-256, single use,
// valid 60 s (session) or 5 min (link).

// HandoffProviderCardioAgent is the only provider today.
const HandoffProviderCardioAgent = "cardioagent"

const (
	handoffPurposeSession   = "session"
	handoffPurposeLink      = "link"
	handoffSessionCodeTTL   = 60 * time.Second
	handoffLinkCodeTTL      = 5 * time.Minute
	handoffCodeKeyPrefix    = "handoff:code:"
	handoffExchangeIPPrefix = "handoff:exchange:ip:"
	handoffExchangeIPLimit  = 30
	handoffExchangeIPWindow = time.Minute
	handoffMaxFieldLength   = 256
	handoffMaxPathLength    = 4096
)

// Handoff errors, mapped to HTTP statuses by the controller.
var (
	ErrHandoffInvalidRequest = errors.New("handoff_invalid_request")
	ErrHandoffNotAllowed     = errors.New("handoff_not_allowed")
	ErrHandoffInvalidCode    = errors.New("handoff_invalid_code")
	ErrHandoffRateLimited    = errors.New("handoff_rate_limited")
	ErrHandoffUnavailable    = errors.New("handoff_unavailable")
)

// HandoffCodeStore keeps one-time codes (Redis).
type HandoffCodeStore interface {
	SetIfAbsent(key string, val interface{}, exp time.Duration) (bool, error)
	GetAndDelete(key string) (string, bool, error)
	IncrementWithExpiry(key string, expiry time.Duration) (int64, time.Duration, error)
}

// HandoffLinkStore keeps account links (Postgres).
type HandoffLinkStore interface {
	FindActiveAccountLink(ctx context.Context, provider, tenantID, externalUID string) (iamRepository.AccountLink, bool, error)
	SaveAccountLink(ctx context.Context, link iamRepository.AccountLink) error
}

// HandoffUsers reads PACS-AI user profiles.
type HandoffUsers interface {
	GetTenantUserByID(ctx context.Context, tenantID, id string) (userServiceTypes.GetTenantUser, error)
}

// HandoffSessions creates PACS-AI sessions exactly as password login does.
type HandoffSessions interface {
	SetTokenSession(ctx context.Context, data types.SetTokenSession) error
}

// HandoffService implements mint, exchange and link.
type HandoffService struct {
	Codes    HandoffCodeStore
	Links    HandoffLinkStore
	Users    HandoffUsers
	Sessions HandoffSessions
	// PublicURL is the PACS-AI address users open (e.g. https://pacs.example.org).
	PublicURL string
	// Random defaults to crypto/rand.
	Random io.Reader
}

// MintHandoffRequest is what the other app's backend sends.
type MintHandoffRequest struct {
	TenantID      string
	ExternalUID   string
	Email         string
	EmailVerified bool
	ReturnPath    string
}

// MintHandoffResult is the URL to open now.
type MintHandoffResult struct {
	URL          string
	ExpiresIn    int
	LinkRequired bool
}

// ExchangeHandoffResult is a new PACS-AI session.
type ExchangeHandoffResult struct {
	SessionToken string
	TenantID     string
	ReturnPath   string
}

type handoffCode struct {
	Purpose     string `json:"purpose"`
	Provider    string `json:"provider"`
	TenantID    string `json:"tenantId"`
	PacsUserID  string `json:"pacsUserId,omitempty"`
	ExternalUID string `json:"externalUid"`
	Email       string `json:"email,omitempty"`
	ReturnPath  string `json:"returnPath"`
}

var handoffCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// Paths the handoff may open: the viewer, the worklist (study list) and home.
var handoffPathRoots = map[string]bool{"": true, "viewer": true, "worklist": true}

// ValidHandoffReturnPath accepts only a relative PACS-AI path on the allowlist.
func ValidHandoffReturnPath(path string) bool {
	if path == "" || len(path) > handoffMaxPathLength || !strings.HasPrefix(path, "/") {
		return false
	}
	if strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\#") || strings.Contains(path, "://") {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	parsed, err := url.Parse(path)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return false
	}
	root := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 2)[0]
	return handoffPathRoots[root]
}

func hashHandoffCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return handoffCodeKeyPrefix + hex.EncodeToString(sum[:])
}

func hashForLog(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func handoffUserAllowed(user userServiceTypes.GetTenantUser) bool {
	if user.ID == "" || user.IsAccountDisabled || user.AccessState == "SUSPENDED" {
		return false
	}
	return user.IsEmailVerified || user.IsAdminCreated
}

func (service *HandoffService) ready() bool {
	return service != nil && service.Codes != nil && service.Links != nil && service.Users != nil &&
		service.Sessions != nil && strings.TrimSpace(service.PublicURL) != ""
}

func (service *HandoffService) newCode(record handoffCode, ttl time.Duration) (string, error) {
	random := service.Random
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	payload, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	stored, err := service.Codes.SetIfAbsent(hashHandoffCode(code), string(payload), ttl)
	if err != nil {
		return "", err
	}
	if !stored {
		return "", errors.New("handoff code collision")
	}
	return code, nil
}

func (service *HandoffService) takeCode(code string) (handoffCode, error) {
	if !handoffCodePattern.MatchString(code) {
		return handoffCode{}, ErrHandoffInvalidCode
	}
	payload, found, err := service.Codes.GetAndDelete(hashHandoffCode(code))
	if err != nil {
		return handoffCode{}, ErrHandoffUnavailable
	}
	if !found {
		return handoffCode{}, ErrHandoffInvalidCode
	}
	var record handoffCode
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return handoffCode{}, ErrHandoffInvalidCode
	}
	return record, nil
}

func (service *HandoffService) url(page, code string) string {
	return strings.TrimRight(strings.TrimSpace(service.PublicURL), "/") + "/auth/" + page + "#code=" + code
}

// MintHandoff returns a one-time URL: a session handoff for a linked user, or
// a link page (LinkRequired) for a user who has not linked yet.
func (service *HandoffService) MintHandoff(ctx context.Context, request MintHandoffRequest) (MintHandoffResult, error) {
	if !service.ready() {
		return MintHandoffResult{}, ErrHandoffUnavailable
	}
	tenantID := strings.TrimSpace(request.TenantID)
	externalUID := strings.TrimSpace(request.ExternalUID)
	email := normalizedLoginEmail(request.Email)
	if tenantID == "" || externalUID == "" || len(tenantID) > handoffMaxFieldLength ||
		len(externalUID) > handoffMaxFieldLength || len(email) > handoffMaxFieldLength ||
		!ValidHandoffReturnPath(request.ReturnPath) {
		return MintHandoffResult{}, ErrHandoffInvalidRequest
	}
	if !request.EmailVerified {
		log.Printf("[security] event=handoff_mint_rejected reason=email_not_verified tenant_id=%s external=%s", tenantID, hashForLog(externalUID))
		return MintHandoffResult{}, ErrHandoffNotAllowed
	}

	link, linked, err := service.Links.FindActiveAccountLink(ctx, HandoffProviderCardioAgent, tenantID, externalUID)
	if err != nil {
		log.Printf("[security] event=handoff_mint_failed reason=link_lookup tenant_id=%s err=%v", tenantID, err)
		return MintHandoffResult{}, ErrHandoffUnavailable
	}
	if !linked {
		code, err := service.newCode(handoffCode{
			Purpose: handoffPurposeLink, Provider: HandoffProviderCardioAgent, TenantID: tenantID,
			ExternalUID: externalUID, Email: email, ReturnPath: request.ReturnPath,
		}, handoffLinkCodeTTL)
		if err != nil {
			log.Printf("[security] event=handoff_mint_failed reason=code_store tenant_id=%s err=%v", tenantID, err)
			return MintHandoffResult{}, ErrHandoffUnavailable
		}
		log.Printf("[security] event=handoff_link_minted tenant_id=%s external=%s", tenantID, hashForLog(externalUID))
		return MintHandoffResult{URL: service.url("link", code), ExpiresIn: int(handoffLinkCodeTTL.Seconds()), LinkRequired: true}, nil
	}

	user, err := service.Users.GetTenantUserByID(ctx, tenantID, link.PacsUserID)
	if err != nil || !handoffUserAllowed(user) {
		log.Printf("[security] event=handoff_mint_rejected reason=user_not_allowed tenant_id=%s user_id=%s", tenantID, link.PacsUserID)
		return MintHandoffResult{}, ErrHandoffNotAllowed
	}
	code, err := service.newCode(handoffCode{
		Purpose: handoffPurposeSession, Provider: HandoffProviderCardioAgent, TenantID: tenantID,
		PacsUserID: link.PacsUserID, ExternalUID: externalUID, ReturnPath: request.ReturnPath,
	}, handoffSessionCodeTTL)
	if err != nil {
		log.Printf("[security] event=handoff_mint_failed reason=code_store tenant_id=%s err=%v", tenantID, err)
		return MintHandoffResult{}, ErrHandoffUnavailable
	}
	log.Printf("[security] event=handoff_minted tenant_id=%s user_id=%s external=%s", tenantID, link.PacsUserID, hashForLog(externalUID))
	return MintHandoffResult{URL: service.url("handoff", code), ExpiresIn: int(handoffSessionCodeTTL.Seconds())}, nil
}

// ExchangeHandoff turns a session code into a normal PACS-AI session.
func (service *HandoffService) ExchangeHandoff(ctx context.Context, code, clientIP string) (ExchangeHandoffResult, error) {
	if !service.ready() {
		return ExchangeHandoffResult{}, ErrHandoffUnavailable
	}
	if ip := strings.TrimSpace(clientIP); ip != "" {
		count, _, err := service.Codes.IncrementWithExpiry(handoffExchangeIPPrefix+ip, handoffExchangeIPWindow)
		if err != nil {
			return ExchangeHandoffResult{}, ErrHandoffUnavailable
		}
		if count > handoffExchangeIPLimit {
			log.Printf("[security] event=handoff_exchange_rate_limited")
			return ExchangeHandoffResult{}, ErrHandoffRateLimited
		}
	}
	record, err := service.takeCode(strings.TrimSpace(code))
	if err != nil {
		if errors.Is(err, ErrHandoffInvalidCode) {
			log.Printf("[security] event=handoff_exchange_rejected reason=invalid_code")
		}
		return ExchangeHandoffResult{}, err
	}
	if record.Purpose != handoffPurposeSession {
		log.Printf("[security] event=handoff_exchange_rejected reason=wrong_purpose tenant_id=%s", record.TenantID)
		return ExchangeHandoffResult{}, ErrHandoffInvalidCode
	}
	// The link may have been revoked since the code was minted.
	link, linked, err := service.Links.FindActiveAccountLink(ctx, record.Provider, record.TenantID, record.ExternalUID)
	if err != nil {
		return ExchangeHandoffResult{}, ErrHandoffUnavailable
	}
	if !linked || link.PacsUserID != record.PacsUserID {
		log.Printf("[security] event=handoff_exchange_rejected reason=link_changed tenant_id=%s", record.TenantID)
		return ExchangeHandoffResult{}, ErrHandoffNotAllowed
	}
	user, err := service.Users.GetTenantUserByID(ctx, record.TenantID, record.PacsUserID)
	if err != nil || !handoffUserAllowed(user) {
		log.Printf("[security] event=handoff_exchange_rejected reason=user_not_allowed tenant_id=%s user_id=%s", record.TenantID, record.PacsUserID)
		return ExchangeHandoffResult{}, ErrHandoffNotAllowed
	}
	sessionToken := generateID()
	if err := service.Sessions.SetTokenSession(ctx, types.SetTokenSession{
		SessionID:           sessionToken,
		TenantID:            record.TenantID,
		UserID:              user.ID,
		Role:                user.Role,
		ExpireTimeInSeconds: entity.ExpireTimeInSeconds,
	}); err != nil {
		if err.Error() == apiError.AccountSuspended {
			return ExchangeHandoffResult{}, ErrHandoffNotAllowed
		}
		return ExchangeHandoffResult{}, ErrHandoffUnavailable
	}
	log.Printf("[security] event=handoff_exchanged tenant_id=%s user_id=%s external=%s", record.TenantID, user.ID, hashForLog(record.ExternalUID))
	return ExchangeHandoffResult{SessionToken: sessionToken, TenantID: record.TenantID, ReturnPath: record.ReturnPath}, nil
}

// CompleteHandoffLink binds the app's user named in a link code to the
// PACS-AI user who is signed in, when both share the same verified email.
func (service *HandoffService) CompleteHandoffLink(ctx context.Context, code, tenantID, userID string) (string, error) {
	if !service.ready() {
		return "", ErrHandoffUnavailable
	}
	tenantID, userID = strings.TrimSpace(tenantID), strings.TrimSpace(userID)
	if tenantID == "" || userID == "" {
		return "", ErrHandoffNotAllowed
	}
	record, err := service.takeCode(strings.TrimSpace(code))
	if err != nil {
		return "", err
	}
	if record.Purpose != handoffPurposeLink || record.TenantID != tenantID {
		log.Printf("[security] event=handoff_link_rejected reason=wrong_code tenant_id=%s user_id=%s", tenantID, userID)
		return "", ErrHandoffInvalidCode
	}
	user, err := service.Users.GetTenantUserByID(ctx, tenantID, userID)
	if err != nil || !handoffUserAllowed(user) {
		return "", ErrHandoffNotAllowed
	}
	// A signed-in user could be sent someone else's link code; linking that
	// code would let its owner sign in as this user. The other app's verified
	// email must therefore be this account's email. The link itself is keyed
	// on the other app's user ID, so a later email change does not move it.
	if record.Email == "" || normalizedLoginEmail(user.Email) != record.Email {
		log.Printf("[security] event=handoff_link_rejected reason=email_mismatch tenant_id=%s user_id=%s", tenantID, userID)
		return "", ErrHandoffNotAllowed
	}
	if err := service.Links.SaveAccountLink(ctx, iamRepository.AccountLink{
		Provider: record.Provider, ExternalUID: record.ExternalUID, TenantID: tenantID,
		PacsUserID: userID, EmailAtLink: record.Email, LinkedBy: "self-password",
	}); err != nil {
		log.Printf("[security] event=handoff_link_failed tenant_id=%s user_id=%s err=%v", tenantID, userID, err)
		return "", ErrHandoffUnavailable
	}
	log.Printf("[security] event=handoff_linked tenant_id=%s user_id=%s external=%s", tenantID, userID, hashForLog(record.ExternalUID))
	return record.ReturnPath, nil
}
