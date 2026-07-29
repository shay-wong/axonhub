package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWithIPRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", WithIPRateLimit(2, time.Minute), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":1234"
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		return resp.Code
	}

	require.Equal(t, http.StatusNoContent, request("192.0.2.1"))
	require.Equal(t, http.StatusNoContent, request("192.0.2.1"))
	require.Equal(t, http.StatusTooManyRequests, request("192.0.2.1"))
	require.Equal(t, http.StatusNoContent, request("192.0.2.2"))
}
