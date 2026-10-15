package server

import (
	"net/http"
	"net/http/httptest"

	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/metrics"
	"github.com/go-chi/chi/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP authentication boundaries", func() {
	It("requires the API key for /debug and not for /metrics", func() {
		router := chi.NewRouter()
		configureDebugHandler(router, "secret")
		metrics.Start(router, config.Metrics{Enable: true, Path: "/metrics"})

		denied := httptest.NewRecorder()
		router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
		Expect(denied.Code).Should(Equal(http.StatusUnauthorized))

		allowed := httptest.NewRecorder()
		allowedReq := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
		allowedReq.Header.Set("X-API-Key", "secret")
		router.ServeHTTP(allowed, allowedReq)
		Expect(allowed.Code).ShouldNot(Equal(http.StatusUnauthorized))

		open := httptest.NewRecorder()
		router.ServeHTTP(open, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		Expect(open.Code).Should(Equal(http.StatusOK))
	})
})
