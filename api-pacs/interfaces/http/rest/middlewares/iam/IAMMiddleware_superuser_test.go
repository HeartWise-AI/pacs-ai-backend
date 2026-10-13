package iam

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirebaseSuperUserGuardRejectsMissingServerConfiguration(t *testing.T) {
	t.Setenv("FIREBASE_SUPERUSER_KEY", "")
	reached := false
	handler := (&IAMMiddleware{}).FirebaseSuperUserGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", nil))

	if recorder.Code != http.StatusUnauthorized || reached {
		t.Fatalf("status=%d reached=%t", recorder.Code, reached)
	}
}

func TestFirebaseSuperUserGuardRequiresExactConfiguredKey(t *testing.T) {
	t.Setenv("FIREBASE_SUPERUSER_KEY", "catalog-secret")
	reached := false
	handler := (&IAMMiddleware{}).FirebaseSuperUserGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("X-FB-SUDO-KEY", "catalog-secret")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !reached {
		t.Fatalf("status=%d reached=%t", recorder.Code, reached)
	}
}
