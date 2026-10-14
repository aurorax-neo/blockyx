package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const (
	apiKeyHeader  = "X-API-Key"
	bearerPrefix  = "Bearer "
	authRealmBody = `{"error":"missing or invalid API key"}`
)

// APIKeyAuthMiddleware requires API key authentication for every request passing
// through it. The key can be sent as X-API-Key header or as
// Authorization: Bearer <key> header. Keys are compared in constant time.
// An empty apiKey disables the check, but the blockyx binary refuses to start
// with an empty key (see cmd/serve.go), so this only happens in library/test use.
func APIKeyAuthMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" || validAPIKey(r, apiKey) {
				next.ServeHTTP(w, r)

				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(authRealmBody))
		})
	}
}

func validAPIKey(r *http.Request, apiKey string) bool {
	expected := []byte(apiKey)

	if subtle.ConstantTimeCompare([]byte(r.Header.Get(apiKeyHeader)), expected) == 1 {
		return true
	}

	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, bearerPrefix) {
		got := []byte(strings.TrimPrefix(auth, bearerPrefix))

		return subtle.ConstantTimeCompare(got, expected) == 1
	}

	return false
}
