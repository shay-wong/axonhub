package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xerrors"
	"github.com/looplj/axonhub/internal/server/biz"
)

// InvitationHandlersParams contains dependencies for invitation HTTP handlers.
type InvitationHandlersParams struct {
	fx.In

	InvitationService *biz.InvitationService
	AuthService       *biz.AuthService
}

// InvitationHandlers serves invitation creation, lookup, and registration endpoints.
type InvitationHandlers struct {
	InvitationService *biz.InvitationService
	AuthService       *biz.AuthService
}

// NewInvitationHandlers creates invitation HTTP handlers.
func NewInvitationHandlers(params InvitationHandlersParams) *InvitationHandlers {
	return &InvitationHandlers{
		InvitationService: params.InvitationService,
		AuthService:       params.AuthService,
	}
}

// CreateInvitationRequest contains the parameters for creating an invitation.
type CreateInvitationRequest struct {
	ExpiresInHours *int `json:"expiresInHours"`
	MaxUses        *int `json:"maxUses"`
	RoleID         int  `json:"roleID" binding:"required"`
}

// InvitationResponse contains invitation metadata returned by the API.
type InvitationResponse struct {
	Token         string     `json:"token,omitempty"`
	ProjectName   string     `json:"projectName"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	MaxUses       int        `json:"maxUses"`
	UsedCount     int        `json:"usedCount"`
	RemainingUses int        `json:"remainingUses"`
}

// RegisterInvitationRequest contains the credentials and profile data for registration.
type RegisterInvitationRequest struct {
	Email     string `json:"email" binding:"required,email"`
	Password  string `json:"password" binding:"required,min=7"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// RegisterInvitationResponse contains the registered user and session token.
type RegisterInvitationResponse struct {
	User  *objects.UserInfo `json:"user"`
	Token string            `json:"token"`
}

// Create handles invitation creation for a project.
func (h *InvitationHandlers) Create(c *gin.Context) {
	projectID, ok := contexts.GetProjectID(c.Request.Context())
	if !ok {
		JSONError(c, http.StatusBadRequest, errors.New("X-Project-ID header is required"))
		return
	}

	var req CreateInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid invitation request"))
		return
	}

	created, err := h.InvitationService.CreateInvitation(
		c.Request.Context(),
		projectID,
		req.RoleID,
		req.ExpiresInHours,
		invitationMaxUses(req.MaxUses),
	)
	if err != nil {
		writeInvitationError(c, err)
		return
	}

	c.JSON(http.StatusOK, invitationResponse(created.Token, created.Info))
}

// Get handles public invitation metadata lookup.
func (h *InvitationHandlers) Get(c *gin.Context) {
	info, err := h.InvitationService.GetInvitation(c.Request.Context(), c.Param("token"))
	if err != nil {
		writeInvitationError(c, err)
		return
	}

	c.JSON(http.StatusOK, invitationResponse("", *info))
}

// Register handles account registration through an invitation.
func (h *InvitationHandlers) Register(c *gin.Context) {
	var req RegisterInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid registration request"))
		return
	}

	registeredUser, err := h.InvitationService.RegisterInvitation(c.Request.Context(), c.Param("token"), req.Email, req.Password, req.FirstName, req.LastName)
	if err != nil {
		writeInvitationError(c, err)
		return
	}

	token, err := h.AuthService.GenerateJWTToken(c.Request.Context(), registeredUser)
	if err != nil {
		log.Error(c.Request.Context(), "failed to create invitation registration session", log.Cause(err))
		JSONError(c, http.StatusInternalServerError, errors.New("failed to create session"))
		return
	}

	c.JSON(http.StatusOK, RegisterInvitationResponse{
		User:  biz.ConvertUserToUserInfo(c.Request.Context(), registeredUser),
		Token: token,
	})
}

func invitationMaxUses(maxUses *int) int {
	if maxUses == nil {
		return 1
	}
	return *maxUses
}

func writeInvitationError(c *gin.Context, err error) {
	status, publicErr := invitationHTTPError(err)
	if status == http.StatusInternalServerError {
		log.Error(c.Request.Context(), "invitation request failed", log.Cause(err))
	}
	JSONError(c, status, publicErr)
}

func invitationHTTPError(err error) (int, error) {
	codedErr, ok := xerrors.IsCodedError(err)
	if !ok {
		return http.StatusInternalServerError, biz.ErrInternal
	}

	publicErr := errors.New(codedErr.Message)
	switch codedErr.Code {
	case xerrors.ErrCodeInvalidInput, xerrors.ErrCodeValidationFailed:
		return http.StatusBadRequest, publicErr
	case xerrors.ErrCodeNotFound:
		return http.StatusNotFound, publicErr
	case xerrors.ErrCodeAlreadyExists, xerrors.ErrCodeDuplicateName:
		return http.StatusConflict, publicErr
	case xerrors.ErrCodeUnauthenticated:
		return http.StatusUnauthorized, publicErr
	case xerrors.ErrCodeForbidden:
		return http.StatusForbidden, publicErr
	default:
		return http.StatusInternalServerError, biz.ErrInternal
	}
}

func invitationResponse(token string, info biz.InvitationInfo) InvitationResponse {
	return InvitationResponse{
		Token:         token,
		ProjectName:   info.ProjectName,
		ExpiresAt:     info.ExpiresAt,
		MaxUses:       info.MaxUses,
		UsedCount:     info.UsedCount,
		RemainingUses: info.RemainingUses,
	}
}
