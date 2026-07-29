package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/pkg/xerrors"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestInvitationHTTPError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{name: "validation", err: xerrors.ValidationError("invalid invitation"), wantStatus: http.StatusBadRequest, wantError: "invalid invitation"},
		{name: "not found", err: xerrors.NotFoundError("invitation"), wantStatus: http.StatusNotFound, wantError: "invitation not found"},
		{name: "conflict", err: xerrors.NewCodedError(xerrors.ErrCodeAlreadyExists, "email already exists"), wantStatus: http.StatusConflict, wantError: "email already exists"},
		{name: "forbidden", err: xerrors.ForbiddenError("permission denied"), wantStatus: http.StatusForbidden, wantError: "permission denied"},
		{name: "internal", err: errors.New("database connection failed"), wantStatus: http.StatusInternalServerError, wantError: biz.ErrInternal.Error()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, publicErr := invitationHTTPError(tt.err)
			require.Equal(t, tt.wantStatus, status)
			require.EqualError(t, publicErr, tt.wantError)
		})
	}
}

func TestCreateInvitationRequestMaxUsesIsOptional(t *testing.T) {
	zero := 0
	one := 1
	require.Equal(t, 1, invitationMaxUses(nil))
	require.Equal(t, 0, invitationMaxUses(&zero))
	require.Equal(t, 1, invitationMaxUses(&one))
}
