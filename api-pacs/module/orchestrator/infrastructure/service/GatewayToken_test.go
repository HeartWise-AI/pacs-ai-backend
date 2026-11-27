package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOrchestratorClientSendsServiceToken(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Service-Token"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewOrchestratorClient("synthetic-token", &http.Client{Timeout: time.Second})
	for _, path := range []string{"/chat/init_chat", "/chat/t1", "/dicom/t1", "/chat/t1/progress"} {
		resp, err := client.Post(srv.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	for i, v := range got {
		if v != "synthetic-token" {
			t.Fatalf("request %d: X-Service-Token = %q", i, v)
		}
	}
}

func TestOrchestratorClientWithoutTokenSendsNoHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["X-Service-Token"]; ok {
			t.Errorf("unexpected X-Service-Token header")
		}
	}))
	defer srv.Close()
	resp, err := NewOrchestratorClient("  ", &http.Client{Timeout: time.Second}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestOrchestratorClientDoesNotFollowCrossHostRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect followed; token header %q", r.Header.Get("X-Service-Token"))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()
	resp, err := NewOrchestratorClient("synthetic-token", &http.Client{Timeout: time.Second}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", resp.StatusCode)
	}
}
