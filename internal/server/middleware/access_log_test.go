package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/log"
)

func TestErrorLogsUseRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		middleware gin.HandlerFunc
		handler    gin.HandlerFunc
	}{
		{
			name:       "access log",
			middleware: AccessLog(),
			handler:    func(c *gin.Context) { c.Status(http.StatusTooManyRequests) },
		},
		{
			name:       "recovery log",
			middleware: Recovery(),
			handler:    func(*gin.Context) { panic("test panic") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "error.log")
			log.SetGlobalConfig(log.Config{Output: "file", File: log.FileConfig{Path: logPath}})
			t.Cleanup(func() { log.SetGlobalConfig(log.Config{Output: "stdio"}) })

			router := gin.New()
			router.Use(tt.middleware)
			router.POST("/auth/invitations/:token/register", tt.handler)

			const token = "secret-invitation-token"
			req := httptest.NewRequest(http.MethodPost, "/auth/invitations/"+token+"/register", nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			data, err := os.ReadFile(logPath)
			require.NoError(t, err)
			require.Contains(t, string(data), "/auth/invitations/:token/register")
			require.NotContains(t, string(data), token)
		})
	}
}
