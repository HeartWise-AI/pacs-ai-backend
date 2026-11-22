package rest

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"api-pacs/interfaces/http/rest/clientip"
	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	iamService "api-pacs/module/iam/infrastructure/service"
)

// HandoffServiceInterface is what the handoff routes need (CA-268).
type HandoffServiceInterface interface {
	MintHandoff(ctx context.Context, request iamService.MintHandoffRequest) (iamService.MintHandoffResult, error)
	ExchangeHandoff(ctx context.Context, code, clientIP string) (iamService.ExchangeHandoffResult, error)
	CompleteHandoffLink(ctx context.Context, code, tenantID, userID string) (string, error)
}

// HandoffController serves the sign-in handoff from another app.
type HandoffController struct {
	Service HandoffServiceInterface
	// ServiceToken authenticates the other app's backend on the internal mint
	// route (PACS_AI_HANDOFF_TOKEN). Empty disables minting.
	ServiceToken      string
	TrustedProxyCIDRs []*net.IPNet
	// AllowedRemoteCIDRs lets another host (CardioAgent outside this server)
	// mint through the public nginx: the request is accepted only when the
	// socket peer is a trusted proxy (TrustedProxyCIDRs) and the client it
	// reports is in one of these networks (PACS_AI_HANDOFF_ALLOWED_CIDRS).
	// Empty: minting stays internal-only.
	AllowedRemoteCIDRs []*net.IPNet
}

const handoffMaxBodyBytes = 16 << 10

type mintHandoffBody struct {
	TenantID      string `json:"tenantId"`
	ExternalUID   string `json:"externalUid"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
	ReturnPath    string `json:"returnPath"`
}

type handoffCodeBody struct {
	Code string `json:"code"`
}

func writeHandoff(w http.ResponseWriter, status int, message string, data interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	response := viewmodels.HTTPResponseVM{
		Status:  status,
		Success: status >= 200 && status < 300,
		Message: message,
		Data:    data,
	}
	response.JSON(w)
}

func writeHandoffError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, iamService.ErrHandoffInvalidRequest):
		writeHandoff(w, http.StatusBadRequest, "Invalid handoff request.", map[string]string{"error": "invalid_request"})
	case errors.Is(err, iamService.ErrHandoffNotAllowed):
		writeHandoff(w, http.StatusForbidden, "Sign-in is not allowed for this account.", map[string]string{"error": "not_allowed"})
	case errors.Is(err, iamService.ErrHandoffInvalidCode):
		writeHandoff(w, http.StatusUnauthorized, "This sign-in link has expired or was already used.", map[string]string{"error": "invalid_code"})
	case errors.Is(err, iamService.ErrHandoffRateLimited):
		w.Header().Set("Retry-After", "60")
		writeHandoff(w, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.", map[string]string{"error": "rate_limited"})
	default:
		writeHandoff(w, http.StatusServiceUnavailable, "Sign-in handoff is unavailable.", map[string]string{"error": "unavailable"})
	}
}

func decodeHandoffBody(r *http.Request, target interface{}) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, handoffMaxBodyBytes))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// proxied is true for requests that came through the public nginx, which
// always sets these headers; the mint route is for callers on the internal
// network only.
func proxied(r *http.Request) bool {
	for _, header := range []string{"X-Real-IP", "X-Forwarded-For", "Forwarded", "CF-Connecting-IP"} {
		if r.Header.Get(header) != "" {
			return true
		}
	}
	return false
}

// allowedRemote is true for a request relayed by a trusted proxy on behalf of
// a client in AllowedRemoteCIDRs. The client address comes from the proxy's
// X-Real-IP only when the socket peer is a trusted proxy (clientip.Resolve).
func (controller *HandoffController) allowedRemote(r *http.Request) bool {
	if len(controller.AllowedRemoteCIDRs) == 0 || len(controller.TrustedProxyCIDRs) == 0 {
		return false
	}
	peer := net.ParseIP(clientip.SocketIP(r))
	if peer == nil || !inNetworks(peer, controller.TrustedProxyCIDRs) {
		return false
	}
	client := net.ParseIP(clientip.Resolve(r, controller.TrustedProxyCIDRs))
	return client != nil && inNetworks(client, controller.AllowedRemoteCIDRs)
}

func inNetworks(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// MintHandoff is POST /v1/internal/auth/handoff, called by the other app's backend.
func (controller *HandoffController) MintHandoff(w http.ResponseWriter, r *http.Request) {
	expected := strings.TrimSpace(controller.ServiceToken)
	if expected == "" || controller.Service == nil {
		writeHandoffError(w, iamService.ErrHandoffUnavailable)
		return
	}
	if proxied(r) && !controller.allowedRemote(r) {
		writeHandoff(w, http.StatusNotFound, "Not found.", nil)
		return
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer"))
	if !strings.HasPrefix(authorization, "Bearer ") ||
		subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
		writeHandoff(w, http.StatusUnauthorized, "Authentication is required.", map[string]string{"error": "unauthorized"})
		return
	}
	var body mintHandoffBody
	if err := decodeHandoffBody(r, &body); err != nil {
		writeHandoffError(w, iamService.ErrHandoffInvalidRequest)
		return
	}
	result, err := controller.Service.MintHandoff(r.Context(), iamService.MintHandoffRequest{
		TenantID:      body.TenantID,
		ExternalUID:   body.ExternalUID,
		Email:         body.Email,
		EmailVerified: body.EmailVerified,
		ReturnPath:    body.ReturnPath,
	})
	if err != nil {
		writeHandoffError(w, err)
		return
	}
	data := map[string]interface{}{"url": result.URL, "expiresIn": result.ExpiresIn}
	if result.LinkRequired {
		data["error"] = "link_required"
		writeHandoff(w, http.StatusConflict, "The user has not linked a PACS-AI account yet.", data)
		return
	}
	writeHandoff(w, http.StatusOK, "Sign-in link created.", data)
}

// ExchangeHandoff is POST /v1/iam/handoff/exchange, called by the PACS-AI viewer.
func (controller *HandoffController) ExchangeHandoff(w http.ResponseWriter, r *http.Request) {
	if controller.Service == nil {
		writeHandoffError(w, iamService.ErrHandoffUnavailable)
		return
	}
	var body handoffCodeBody
	if err := decodeHandoffBody(r, &body); err != nil {
		writeHandoffError(w, iamService.ErrHandoffInvalidCode)
		return
	}
	result, err := controller.Service.ExchangeHandoff(r.Context(), body.Code, clientip.Resolve(r, controller.TrustedProxyCIDRs))
	if err != nil {
		writeHandoffError(w, err)
		return
	}
	writeHandoff(w, http.StatusOK, "Signed in.", map[string]string{
		"sessionToken": result.SessionToken,
		"tenantId":     result.TenantID,
		"returnPath":   result.ReturnPath,
	})
}

// CompleteHandoffLink is POST /v1/iam/handoff/link, for a signed-in user.
func (controller *HandoffController) CompleteHandoffLink(w http.ResponseWriter, r *http.Request) {
	if controller.Service == nil {
		writeHandoffError(w, iamService.ErrHandoffUnavailable)
		return
	}
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID, _ := r.Context().Value(iamTypes.UserIDCtx).(string)
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(userID) == "" {
		writeHandoff(w, http.StatusUnauthorized, "Authentication is required.", map[string]string{"error": "unauthorized"})
		return
	}
	var body handoffCodeBody
	if err := decodeHandoffBody(r, &body); err != nil {
		writeHandoffError(w, iamService.ErrHandoffInvalidCode)
		return
	}
	returnPath, err := controller.Service.CompleteHandoffLink(r.Context(), body.Code, tenantID, userID)
	if err != nil {
		writeHandoffError(w, err)
		return
	}
	writeHandoff(w, http.StatusOK, "Accounts linked.", map[string]string{"returnPath": returnPath})
}
