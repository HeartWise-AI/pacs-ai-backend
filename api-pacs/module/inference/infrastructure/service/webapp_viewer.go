package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	redisTypes "api-pacs/infrastructures/database/redis/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

const webAppViewerPathPrefix = "/app/viewer/"
const webAppViewerGrantTTL = 30 * time.Minute
const webAppViewerGrantKeyPrefix = "inference:webapp-viewer:"

var webAppViewerToken = regexp.MustCompile(`^[a-f0-9]{64}$`)
var webAppViewerContainerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// WebAppViewerGrant binds a viewer token to the tenant registration and the
// container that produced a WEB_APP prediction.
type WebAppViewerGrant struct {
	TenantID      string `json:"tenantId"`
	ContainerID   string `json:"containerId"`
	ContainerName string `json:"containerName"`
}

// WebAppViewerGrantStore issues and looks up short-lived viewer tokens.
type WebAppViewerGrantStore interface {
	Issue(grant WebAppViewerGrant) (string, error)
	Lookup(token string) (WebAppViewerGrant, error)
}

// RedisWebAppViewerGrants keeps viewer grants in Redis until they expire.
type RedisWebAppViewerGrants struct {
	RedisDBHandlerInterface redisTypes.RedisDBHandlerInterface
}

func (grants *RedisWebAppViewerGrants) Issue(grant WebAppViewerGrant) (string, error) {
	if grants.RedisDBHandlerInterface == nil {
		return "", errors.New(apiError.ServerError)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	value, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	if err := grants.RedisDBHandlerInterface.Set(webAppViewerGrantKeyPrefix+token, string(value), webAppViewerGrantTTL); err != nil {
		return "", err
	}
	return token, nil
}

func (grants *RedisWebAppViewerGrants) Lookup(token string) (WebAppViewerGrant, error) {
	if grants.RedisDBHandlerInterface == nil || !webAppViewerToken.MatchString(token) {
		return WebAppViewerGrant{}, errors.New(apiError.UnauthorizedAccess)
	}
	value, err := grants.RedisDBHandlerInterface.Get(webAppViewerGrantKeyPrefix + token)
	if err != nil {
		return WebAppViewerGrant{}, errors.New(apiError.UnauthorizedAccess)
	}
	var grant WebAppViewerGrant
	if err := json.Unmarshal([]byte(value), &grant); err != nil {
		return WebAppViewerGrant{}, errors.New(apiError.UnauthorizedAccess)
	}
	return grant, nil
}

// grantWebAppViewer replaces the model's webappPath with one that carries a
// viewer token, so the browser frame can load the viewer without a session
// header. The token is bound to the container that served this prediction.
func (service *InferenceCommandService) grantWebAppViewer(tenantID, containerID, containerName string, data map[string]interface{}) error {
	webappPath, ok := data["webappPath"].(string)
	if !ok || !strings.HasPrefix(webappPath, webAppViewerPathPrefix) {
		return nil
	}
	if service.WebAppViewerGrants == nil {
		return errors.New(apiError.ServerError)
	}
	token, err := service.WebAppViewerGrants.Issue(WebAppViewerGrant{
		TenantID: tenantID, ContainerID: containerID, ContainerName: containerName,
	})
	if err != nil {
		return errors.New(apiError.ServerError)
	}
	data["webappPath"] = "/" + token + webappPath
	return nil
}

// ResolveWebAppViewer returns the container name a viewer token may be
// proxied to. The grant's registration must still exist with WEB_APP output,
// and the running container must be the one recorded when it was issued.
func (service *InferenceCommandService) ResolveWebAppViewer(ctx context.Context, token, containerName string) (string, error) {
	if service.WebAppViewerGrants == nil {
		return "", errors.New(apiError.UnauthorizedAccess)
	}
	grant, err := service.WebAppViewerGrants.Lookup(token)
	if err != nil {
		return "", errors.New(apiError.UnauthorizedAccess)
	}
	if grant.TenantID == "" || grant.ContainerID == "" || grant.ContainerName != containerName ||
		!webAppViewerContainerName.MatchString(grant.ContainerName) {
		return "", errors.New(apiError.ForbiddenAccess)
	}
	model, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, grant.TenantID, grant.ContainerID)
	if err != nil {
		if err.Error() == apiError.MissingRecord {
			return "", errors.New(apiError.ForbiddenAccess)
		}
		return "", err
	}
	if model.TenantID != grant.TenantID || model.ContainerID != grant.ContainerID || model.OutputMode != entity.OutputModeWebApp {
		return "", errors.New(apiError.ForbiddenAccess)
	}
	info, err := service.DockerSDKInterface.GetContainerInfo(ctx, grant.ContainerID)
	if err != nil {
		return "", errors.New(apiError.DockerError)
	}
	if info.ID != grant.ContainerID || info.Lifecycle != dockerTypes.LifecycleRunning ||
		strings.TrimPrefix(info.Name, "/") != grant.ContainerName {
		return "", errors.New(apiError.ForbiddenAccess)
	}
	return grant.ContainerName, nil
}
