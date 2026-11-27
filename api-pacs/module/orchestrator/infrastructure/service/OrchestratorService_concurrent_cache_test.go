package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-pacs/module/orchestrator/domain/entity"
	"api-pacs/module/orchestrator/infrastructure/service/types"
)

const (
	concurrentCacheCallers = 64
	concurrentCacheRounds  = 20
)

// chatBackend is a stand-in for the external chat service. hold is how many
// requests must arrive before any of them is answered, so the callers reach
// the local thread cache together.
func chatBackend(t *testing.T, hold int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	var seq atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hold > 0 {
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
		}
		writeChatBackendFixture(w, r, seq.Add(1))
	}))
	t.Cleanup(server.Close)
	return server
}

func writeChatBackendFixture(w http.ResponseWriter, r *http.Request, n int64) {
	switch {
	case r.URL.Path == "/chat/init_chat":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"conversation_id":"thread-%d"}`, n)
	case strings.HasPrefix(r.URL.Path, "/chat/"):
		id := strings.TrimPrefix(r.URL.Path, "/chat/")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"thread_id":%q,"status":"success","message":{"role":"assistant","content":"ok"},"message_id":"answer-%d"}`, id, n)
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
}

func sharedOrchestrator(t *testing.T, backend *httptest.Server) *OrchestratorService {
	t.Helper()
	client := backend.Client()
	client.Timeout = 5 * time.Second
	return &OrchestratorService{
		OrchestratorAPIURL: backend.URL,
		OrchestratorClient: client,
		ThreadsByID:        map[string]*entity.Thread{},
	}
}

func runTogether(t *testing.T, n int, fn func(i int) error) {
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
		t.Errorf("concurrent orchestrator call: %v", err)
	}
}

func TestConcurrentCreateThreadKeepsTheProcessAlive(t *testing.T) {
	service := &OrchestratorService{ThreadsByID: map[string]*entity.Thread{}}
	for round := 0; round < concurrentCacheRounds; round++ {
		backend := chatBackend(t, concurrentCacheCallers)
		service.OrchestratorAPIURL = backend.URL
		service.OrchestratorClient = backend.Client()
		service.OrchestratorClient.Timeout = 5 * time.Second
		runTogether(t, concurrentCacheCallers, func(i int) error {
			_, err := service.CreateThread(orchestratorTestContext(), types.CreateThreadRequest{
				Metadata: map[string]interface{}{"n": i},
			})
			return err
		})
	}
}

func TestConcurrentCreateMessageOnUncachedThreadsKeepsTheProcessAlive(t *testing.T) {
	service := &OrchestratorService{ThreadsByID: map[string]*entity.Thread{}}
	for round := 0; round < concurrentCacheRounds; round++ {
		backend := chatBackend(t, concurrentCacheCallers)
		service.OrchestratorAPIURL = backend.URL
		service.OrchestratorClient = backend.Client()
		service.OrchestratorClient.Timeout = 5 * time.Second
		runTogether(t, concurrentCacheCallers, func(i int) error {
			_, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
				ThreadID: fmt.Sprintf("thread-%d-%d", round, i),
				Content:  "hello",
			})
			return err
		})
	}
}

func TestConcurrentCreateMessageOnOneCachedThreadKeepsTheProcessAlive(t *testing.T) {
	seedBackend := chatBackend(t, 0)
	service := sharedOrchestrator(t, seedBackend)
	if _, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-shared",
		Content:  "seed",
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	for round := 0; round < concurrentCacheRounds; round++ {
		backend := chatBackend(t, concurrentCacheCallers)
		service.OrchestratorAPIURL = backend.URL
		service.OrchestratorClient = backend.Client()
		service.OrchestratorClient.Timeout = 5 * time.Second
		runTogether(t, concurrentCacheCallers, func(int) error {
			_, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
				ThreadID: "thread-shared",
				Content:  "again",
				Metadata: map[string]interface{}{"source": "viewer"},
			})
			return err
		})
	}
}

func TestConcurrentUploadDicomPayloadOnOneCachedThreadKeepsTheProcessAlive(t *testing.T) {
	service := &OrchestratorService{
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
	threadID := "thread-shared"
	for round := 0; round < concurrentCacheRounds; round++ {
		backend := chatBackend(t, concurrentCacheCallers)
		service.OrchestratorAPIURL = backend.URL
		service.OrchestratorClient = backend.Client()
		service.OrchestratorClient.Timeout = 5 * time.Second
		runTogether(t, concurrentCacheCallers, func(i int) error {
			_, err := service.UploadDicomPayload(orchestratorTestContext(), types.DicomPayloadRequest{
				ThreadID: &threadID,
				Payload: []types.StudyData{{
					StudyInstanceUID:   "1.2.3",
					SeriesInstanceUIDs: []string{"4.5.6"},
					AdditionalMetadata: map[string]interface{}{fmt.Sprintf("k%d", i): i},
				}},
			})
			return err
		})
	}
}

func TestConcurrentGetThreadDuringCreateThreadKeepsTheProcessAlive(t *testing.T) {
	const writers = concurrentCacheCallers / 2
	const readers = concurrentCacheCallers / 2
	service := &OrchestratorService{
		ThreadsByID: map[string]*entity.Thread{
			"thread-seed": {
				ID:        "thread-seed",
				Messages:  []entity.Message{{ID: "m", ThreadID: "thread-seed", Content: "seed", Role: "user"}},
				Metadata:  map[string]interface{}{"studyInstanceUID": "1.2.3"},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}

	for round := 0; round < concurrentCacheRounds; round++ {
		backend := chatBackend(t, writers+readers)
		service.OrchestratorAPIURL = backend.URL
		service.OrchestratorClient = backend.Client()
		service.OrchestratorClient.Timeout = 5 * time.Second

		var start sync.WaitGroup
		start.Add(1)
		var done sync.WaitGroup
		done.Add(writers + readers)
		errCh := make(chan error, writers+readers)
		for i := 0; i < writers; i++ {
			go func() {
				defer done.Done()
				start.Wait()
				_, err := service.CreateThread(orchestratorTestContext(), types.CreateThreadRequest{})
				if err != nil {
					errCh <- err
				}
			}()
		}
		for i := 0; i < readers; i++ {
			go func() {
				defer done.Done()
				start.Wait()
				_, err := service.GetThread(orchestratorTestContext(), "thread-seed")
				if err != nil {
					errCh <- err
				}
			}()
		}
		start.Done()
		done.Wait()
		close(errCh)
		for err := range errCh {
			t.Errorf("concurrent orchestrator call: %v", err)
		}
	}
}
