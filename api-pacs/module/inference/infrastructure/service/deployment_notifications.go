package service

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// RunDeploymentNotifications reconciles at startup and after registry changes.
// Delivery is retried until acknowledged. study-service also pulls periodically,
// so a lost notification or either startup order cannot strand a stale cache.
func (service *InferenceCommandService) RunDeploymentNotifications(ctx context.Context, baseURL, token string) {
	service.runDeploymentNotifications(ctx, baseURL, token, 30*time.Second)
}

func (service *InferenceCommandService) runDeploymentNotifications(ctx context.Context, baseURL, token string, interval time.Duration) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(token) == "" {
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	lastRevision := ""
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		registry, err := service.ActiveDeployments(attemptCtx)
		if err != nil {
			log.Print("[deployment-registry] registry reconciliation failed; will retry")
		}
		if err == nil && registry.Revision != lastRevision {
			request, reqErr := http.NewRequestWithContext(attemptCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/ingest/deployments/refresh", nil)
			if reqErr == nil {
				request.Header.Set("Authorization", "Bearer "+token)
				response, callErr := client.Do(request)
				if callErr == nil {
					io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
					response.Body.Close()
					if response.StatusCode == http.StatusOK {
						lastRevision = registry.Revision
					}
				}
			}
			if lastRevision != registry.Revision {
				log.Print("[deployment-registry] refresh notification pending; will retry")
			}
		}
		cancel()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
