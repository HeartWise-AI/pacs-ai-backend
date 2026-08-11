package peeraddr

import (
	"context"
	"net/http"
)

type socketPeerContextKey struct{}

// Capture stores the original socket peer before any middleware can rewrite
// Request.RemoteAddr from forwarded headers.
func Capture(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := context.WithValue(request.Context(), socketPeerContextKey{}, request.RemoteAddr)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// SocketAddress returns the immutable socket peer captured at the start of the
// middleware chain. The fallback supports direct controller invocation in tests.
func SocketAddress(request *http.Request) string {
	if address, ok := request.Context().Value(socketPeerContextKey{}).(string); ok {
		return address
	}
	return request.RemoteAddr
}
