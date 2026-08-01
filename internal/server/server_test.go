package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/looplj/axonhub/internal/server/middleware"
)

func TestMemorySizeUnmarshalText(t *testing.T) {
	tests := []struct {
		input string
		want  MemorySize
	}{
		{input: "512mb", want: 512 << 20},
		{input: "512MB", want: 512 << 20},
		{input: "1.5gb", want: 3 << 29},
		{input: "64k", want: 64 << 10},
		{input: "536870912", want: 512 << 20},
		{input: "", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var got MemorySize
			if err := got.UnmarshalText([]byte(tt.input)); err != nil {
				t.Fatalf("UnmarshalText(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("UnmarshalText(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}

	for _, input := range []string{"512TB", "invalid"} {
		t.Run(input, func(t *testing.T) {
			var got MemorySize
			if err := got.UnmarshalText([]byte(input)); err == nil {
				t.Fatalf("UnmarshalText(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestNewDisablesTrustedProxyHeadersByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := New(Config{Debug: true})
	srv.Use(middleware.WithIPAccessControl(newServerTestIPAccessControlConfig(t)))
	srv.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "192.168.1.1")

	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (spoofed forwarded header must not bypass IP access control)", recorder.Code, http.StatusNotFound)
	}
}

func TestNewHonorsConfiguredTrustedProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := New(Config{Debug: true, TrustedProxies: []string{"10.0.0.1"}})
	srv.Use(middleware.WithIPAccessControl(newServerTestIPAccessControlConfig(t)))
	srv.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "192.168.1.1")

	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (configured trusted proxy should allow forwarded client IP)", recorder.Code, http.StatusNoContent)
	}
}

func newServerTestIPAccessControlConfig(t *testing.T) *middleware.IPAccessControlConfig {
	t.Helper()

	config, err := middleware.NewIPAccessControlConfig(true, []string{"192.168.1.0/24"}, "")
	if err != nil {
		t.Fatalf("NewIPAccessControlConfig() error = %v", err)
	}
	return config
}
