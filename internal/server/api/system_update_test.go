package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestSystemUpdateHandlersRequireWriteSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandlers{}

	for _, test := range []struct {
		name    string
		method  string
		path    string
		handler gin.HandlerFunc
	}{
		{name: "install", method: http.MethodPost, path: "/admin/system/update", handler: handler.InstallUpdate},
		{name: "rollback versions", method: http.MethodGet, path: "/admin/system/rollback-versions", handler: handler.GetRollbackVersions},
		{name: "rollback", method: http.MethodPost, path: "/admin/system/rollback", handler: handler.Rollback},
		{name: "restart", method: http.MethodPost, path: "/admin/system/restart", handler: handler.Restart},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)
			context, _ := gin.CreateTestContext(recorder)
			context.Request = request

			test.handler(context)

			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.JSONEq(t, `{"error":{"type":"Forbidden","message":"permission denied"}}`, recorder.Body.String())
		})
	}

	t.Run("restart rejects unsupported build", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/admin/system/restart", nil)
		request = request.WithContext(contexts.WithUser(request.Context(), &ent.User{IsOwner: true}))
		context, _ := gin.CreateTestContext(recorder)
		context.Request = request
		handler := &SystemHandlers{UpdateService: biz.NewUpdateService()}

		handler.Restart(context)

		require.Equal(t, http.StatusConflict, recorder.Code)
		require.Contains(t, recorder.Body.String(), biz.ErrUpdateUnsupported.Error())
	})
}
