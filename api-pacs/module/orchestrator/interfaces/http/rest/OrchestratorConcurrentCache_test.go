package rest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/orchestrator/domain/entity"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
)

const concurrentHandlerCallers = 64

func TestConcurrentOrchestratorHandlersKeepTheProcessAlive(t *testing.T) {
	service := &orchestratorService.OrchestratorService{
		ThreadsByID: map[string]*entity.Thread{},
	}
	api := httptest.NewServer(orchestratorRouter(service))
	t.Cleanup(api.Close)
	httpClient := api.Client()
	httpClient.Timeout = 5 * time.Second
	half := concurrentHandlerCallers / 2

	for round := 0; round < 20; round++ {
		backend := holdChatBackend(t, concurrentHandlerCallers)
		client := backend.Client()
		client.Timeout = 5 * time.Second
		service.OrchestratorAPIURL = backend.URL
		service.OrchestratorClient = client
		runHandlerTogether(t, concurrentHandlerCallers, func(i int) error {
			var response *http.Response
			var err error
			if i < half {
				response, err = httpClient.Post(api.URL+"/v1/orchestrator/threads", "application/json", bytes.NewBufferString(`{"metadata":{"source":"viewer"}}`))
			} else {
				response, err = httpClient.Post(
					fmt.Sprintf("%s/v1/orchestrator/threads/thread-%d-%d/chat", api.URL, round, i),
					"application/json",
					bytes.NewBufferString(`{"message":"hello"}`),
				)
			}
			if err != nil {
				return err
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("status %d: %s", response.StatusCode, body)
			}
			if !bytes.Contains(body, []byte(`"success":true`)) {
				return fmt.Errorf("response was not successful: %s", body)
			}
			return nil
		})
	}
}

func TestConcurrentDicomAndGetHandlersKeepTheProcessAlive(t *testing.T) {
	service := &orchestratorService.OrchestratorService{
		ThreadsByID: map[string]*entity.Thread{
			"thread-shared": {
				ID:        "thread-shared",
				Messages:  []entity.Message{},
				Metadata:  map[string]interface{}{},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}
	api := httptest.NewServer(orchestratorRouter(service))
	t.Cleanup(api.Close)
	httpClient := api.Client()
	httpClient.Timeout = 5 * time.Second
	body := `{"payload":[{"studyInstanceUID":"1.2.3","seriesInstanceUIDs":["4.5.6"],"additionalMetadata":{"marker":"a"}}]}`

	// Several bursts. One overlap is enough to abort the process; repeating
	// keeps a run from slipping through when the writes miss each other.
	for round := 0; round < 20; round++ {
		backend := holdChatBackend(t, concurrentHandlerCallers)
		client := backend.Client()
		client.Timeout = 5 * time.Second
		service.OrchestratorAPIURL = backend.URL
		service.OrchestratorClient = client
		runHandlerTogether(t, concurrentHandlerCallers, func(i int) error {
			var response *http.Response
			var err error
			if i%2 == 0 {
				response, err = httpClient.Post(api.URL+"/v1/orchestrator/threads/thread-shared/dicom", "application/json", bytes.NewBufferString(body))
			} else {
				response, err = httpClient.Get(api.URL + "/v1/orchestrator/threads/thread-shared")
			}
			if err != nil {
				return err
			}
			defer response.Body.Close()
			payload, _ := io.ReadAll(response.Body)
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("status %d: %s", response.StatusCode, payload)
			}
			return nil
		})
	}
}

func orchestratorRouter(service *orchestratorService.OrchestratorService) http.Handler {
	controller := NewOrchestratorController(service)
	router := chi.NewRouter()
	router.Use(middleware.Recoverer)
	router.Route("/v1/orchestrator", func(r chi.Router) {
		r.Use(signedInSession)
		r.Post("/threads", controller.CreateThread)
		r.Get("/threads/{threadID}", controller.GetThread)
		r.Post("/threads/{threadID}/chat", controller.CreateMessage)
		r.Post("/threads/{threadID}/dicom", controller.UploadDicomPayload)
	})
	return router
}

func signedInSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, "tenant-123")
		ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-123")
		ctx = context.WithValue(ctx, iamTypes.RoleCtx, "user")
		ctx = context.WithValue(ctx, iamTypes.BearerTokenCtx, "test-session")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func holdChatBackend(t *testing.T, hold int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	var seq atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived++
		if arrived == hold {
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			http.Error(w, "chat backend held the response too long", http.StatusGatewayTimeout)
			return
		}
		switch {
		case r.URL.Path == "/chat/init_chat":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"conversation_id":"thread-%d"}`, seq.Add(1))
		case strings.HasPrefix(r.URL.Path, "/chat/"):
			id := strings.TrimPrefix(r.URL.Path, "/chat/")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"thread_id":%q,"status":"success","message":{"role":"assistant","content":"ok"},"message_id":"answer-%d"}`, id, seq.Add(1))
		case strings.HasPrefix(r.URL.Path, "/dicom/"):
			id := strings.TrimPrefix(r.URL.Path, "/dicom/")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"conversation_id":%q,"status":"success","message":"ok"}`, id)
		case strings.HasPrefix(r.URL.Path, "/threads/"):
			id := strings.TrimPrefix(r.URL.Path, "/threads/")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"thread_id":%q,"has_dicom_payload":false,"status":"ok"}`, id)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runHandlerTogether(t *testing.T, n int, fn func(i int) error) {
	t.Helper()
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			start.Wait()
			if err := fn(i); err != nil {
				errCh <- err
			}
		}(i)
	}
	start.Done()
	done.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent orchestrator handler: %v", err)
	}
}
