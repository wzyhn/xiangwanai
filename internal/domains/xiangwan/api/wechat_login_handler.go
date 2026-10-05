package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	identitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxWeChatLoginBodyBytes = 2 * 1024

type weChatLoginApplication interface {
	Login(context.Context, string) (identity.LoginResult, error)
}

// consumerIdentityReader supplies the Auth-owned nickname/avatar projection
// that rides along with a successful login so the client skips one follow-up
// profile read.
type consumerIdentityReader interface {
	ReadConsumerIdentity(
		context.Context,
		uuid.UUID,
	) (nickname string, avatarURL string, err error)
}

type WeChatLoginHandler struct {
	service              weChatLoginApplication
	appID                string
	privacyPolicyVersion string
	identityReader       consumerIdentityReader
}

func NewWeChatLoginHandler(
	service weChatLoginApplication,
	appID string,
	privacyPolicyVersion string,
	identityReader consumerIdentityReader,
) *WeChatLoginHandler {
	return &WeChatLoginHandler{
		service:              service,
		appID:                appID,
		privacyPolicyVersion: privacyPolicyVersion,
		identityReader:       identityReader,
	}
}

func (handler *WeChatLoginHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/auth/wechat/login", handler.Login)
}

// Login exchanges one WeChat code using server-owned credentials and returns
// only the AppID/product/audience-bound Xiangwan consumer session. This narrow
// standalone handler intentionally shares the canonical route path with the
// central executable, whose separate operation remains the global Swagger
// authority; per-runtime response details live in this package README.
func (handler *WeChatLoginHandler) Login(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.appID == "" {
		writeError(c, errx.NewInternal("xiangwan Login is unavailable"))
		return
	}
	if handler.privacyPolicyVersion == "" {
		writeError(c, errx.NewForbidden("Login requires a published privacy policy"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" ||
		len(c.Request.Header.Values("Authorization")) != 0 ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid Login request"))
		return
	}
	payload, err := decodeWeChatLoginRequest(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if payload.AppID != handler.appID {
		writeError(c, errx.NewBadRequest("invalid Login app_id"))
		return
	}
	result, err := handler.service.Login(c.Request.Context(), payload.Code)
	if err != nil {
		writeWeChatLoginError(c, err)
		return
	}
	nickname := ""
	avatarURL := ""
	if handler.identityReader != nil {
		nickname, avatarURL, err = handler.identityReader.ReadConsumerIdentity(
			c.Request.Context(),
			result.PrincipalID,
		)
		if err != nil {
			writeConsumerIdentityError(c, err)
			return
		}
	}
	response.OK(c, WeChatLoginResponse{
		PrincipalID:          result.PrincipalID.String(),
		Token:                result.Token.Value,
		AccessToken:          result.Token.Value,
		TokenType:            "Bearer",
		ExpiresAt:            formatMyRegistrationTime(result.Token.ExpiresAt),
		IsNew:                result.IsNew,
		AppID:                handler.appID,
		PrivacyPolicyVersion: handler.privacyPolicyVersion,
		Nickname:             nickname,
		AvatarURL:            avatarURL,
		Profile: WeChatLoginProfile{
			PrincipalID: result.PrincipalID.String(),
			Role:        "consumer",
			Status:      "active",
		},
	})
}

type WeChatLoginHTTPRequest struct {
	AppID string `json:"app_id"`
	Code  string `json:"code"`
}

type WeChatLoginResponse struct {
	PrincipalID          string             `json:"principal_id"`
	Token                string             `json:"token"`
	AccessToken          string             `json:"access_token"`
	TokenType            string             `json:"token_type"`
	ExpiresAt            string             `json:"expires_at"`
	IsNew                bool               `json:"is_new"`
	AppID                string             `json:"app_id"`
	PrivacyPolicyVersion string             `json:"privacy_policy_version"`
	Nickname             string             `json:"nickname"`
	AvatarURL            string             `json:"avatar_url,omitempty"`
	Profile              WeChatLoginProfile `json:"profile"`
}

type WeChatLoginProfile struct {
	PrincipalID string `json:"principal_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

func decodeWeChatLoginRequest(
	c *gin.Context,
) (WeChatLoginHTTPRequest, error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxWeChatLoginBodyBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var payload WeChatLoginHTTPRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return WeChatLoginHTTPRequest{}, errx.New(
				errx.CodeFileTooLarge,
				"Login request is too large",
			)
		}
		return WeChatLoginHTTPRequest{},
			errx.NewBadRequest("invalid Login request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF ||
		payload.AppID == "" || payload.AppID != strings.TrimSpace(payload.AppID) ||
		!identity.ValidWeChatLoginCode(payload.Code) {
		return WeChatLoginHTTPRequest{},
			errx.NewBadRequest("invalid Login request body")
	}
	return payload, nil
}

func writeWeChatLoginError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidLoginRequest):
		writeError(c, errx.NewBadRequest("invalid Login request"))
	case errors.Is(err, identity.ErrWeChatCodeRejected):
		writeError(c, errx.NewUnauthorized("WeChat login code is invalid or expired"))
	case errors.Is(err, identitypostgres.ErrPrincipalUnavailable):
		writeError(c, errx.NewForbidden("account is unavailable"))
	case errors.Is(err, identitypostgres.ErrIdentityConflict):
		writeError(c, errx.NewConflict("WeChat identity changed; restart login"))
	case errors.Is(err, identitypostgres.ErrIdentityTransactionConflict),
		errors.Is(err, identitypostgres.ErrIdentityGenerationInactive):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, identity.ErrWeChatUnavailable):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Login failed"))
	}
}
