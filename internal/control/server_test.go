package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersAllowRoutingPreferencePutPreflight(t *testing.T) {
	server := &Server{}
	handler := server.securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight should not reach the wrapped handler")
	}))

	request := httptest.NewRequest(http.MethodOptions, "/v1/routing-preference", nil)
	request.Header.Set("Origin", "app://-")
	request.Header.Set("Access-Control-Request-Method", http.MethodPut)
	request.Header.Set("Access-Control-Request-Headers", "content-type,x-codex-mux-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "app://-" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "app://-")
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPut) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want PUT", got)
	}
}
