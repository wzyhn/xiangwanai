package xiangwanapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	adminPrincipalContextKey = "xiangwan_admin_principal"
	adminOIDCExchangeTimeout = 15 * time.Second
)

type adminSessionApplication interface {
	BeginLogin(context.Context, string, string) (xiangwanadmin.LoginStart, error)
	ClaimLogin(context.Context, string, string) (xiangwanadmin.PendingLogin, error)
	FinishLogin(
		context.Context,
		xiangwanadmin.PendingLogin,
		xiangwanadmin.VerifiedIdentity,
		string,
	) (xiangwanadmin.SessionCredentials, error)
	ResolveSession(context.Context, string) (xiangwanadmin.Principal, error)
	RevokeSession(context.Context, uuid.UUID, uuid.UUID, string) error
}

type AdminAuthHandler struct {
	sessions       adminSessionApplication
	oidc           xiangwanadmin.OIDCClient
	allowedOrigins map[string]struct{}
}

// NewDisabledAdminAuthHandler exposes only the fail-closed status/login
// surface when a customer OIDC provider has not been configured.
func NewDisabledAdminAuthHandler() *AdminAuthHandler {
	return &AdminAuthHandler{}
}

func NewAdminAuthHandler(
	sessions adminSessionApplication,
	oidcClient xiangwanadmin.OIDCClient,
	allowedOrigins []string,
) (*AdminAuthHandler, error) {
	if sessions == nil || oidcClient == nil || len(allowedOrigins) == 0 {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	normalized := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		value, err := normalizeAdminOrigin(origin)
		if err != nil {
			return nil, xiangwanadmin.ErrInvalidAdminConfiguration
		}
		normalized[value] = struct{}{}
	}
	return &AdminAuthHandler{
		sessions: sessions, oidc: oidcClient, allowedOrigins: normalized,
	}, nil
}

func (handler *AdminAuthHandler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.GET("/auth/status", handler.Status)
	group.GET("/auth/login", handler.Login)
	group.GET("/auth/callback", handler.Callback)
}

func (handler *AdminAuthHandler) RegisterProtectedRoutes(group *gin.RouterGroup) {
	group.POST("/auth/logout", handler.Logout)
}

func (handler *AdminAuthHandler) RequireSessionAndCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		if handler == nil || handler.sessions == nil {
			writeAdminError(c, xiangwanadmin.ErrInvalidAdminConfiguration)
			c.Abort()
			return
		}
		token, err := c.Cookie(xiangwanadmin.SessionCookieName)
		if err != nil || token == "" {
			writeAdminError(c, xiangwanadmin.ErrSessionInvalid)
			c.Abort()
			return
		}
		principal, err := handler.sessions.ResolveSession(c.Request.Context(), token)
		if err != nil {
			writeAdminError(c, err)
			c.Abort()
			return
		}
		c.Set(adminPrincipalContextKey, principal)
		if adminWriteMethod(c.Request.Method) {
			if err := handler.verifyCSRF(c, principal); err != nil {
				writeAdminError(c, err)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// Status godoc
// @Summary Read Xiangwan administrator authentication status
// @Description Returns fail-closed OIDC availability and, when present, the live PostgreSQL-backed administrator session and Grants.
// @Tags xiangwan-admin
// @Produce json
// @Success 200 {object} AdminAuthStatusResponse
// @Failure 400 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/admin/auth/status [get]
func (handler *AdminAuthHandler) Status(c *gin.Context) {
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid administrator status request"))
		return
	}
	result := AdminAuthStatusResponse{
		Enabled: handler != nil && handler.oidc != nil,
		Grants:  []AdminGrantResponse{},
	}
	if handler == nil || handler.sessions == nil {
		response.OK(c, result)
		return
	}
	token, err := c.Cookie(xiangwanadmin.SessionCookieName)
	if err != nil || token == "" {
		response.OK(c, result)
		return
	}
	principal, err := handler.sessions.ResolveSession(c.Request.Context(), token)
	if errors.Is(err, xiangwanadmin.ErrSessionInvalid) ||
		errors.Is(err, xiangwanadmin.ErrScopeForbidden) {
		handler.clearCookies(c)
		response.OK(c, result)
		return
	}
	if err != nil {
		writeAdminError(c, err)
		return
	}
	result.Authenticated = true
	result.PrincipalID = principal.PrincipalID.String()
	result.ExpiresAt = principal.AbsoluteExpiry.UTC().Format(time.RFC3339)
	result.Grants = projectAdminGrants(principal.Grants)
	response.OK(c, result)
}

// Login godoc
// @Summary Start Xiangwan administrator OIDC login
// @Description Starts Authorization Code + PKCE with a server-owned redirect and bounded local return path. No provider token is persisted.
// @Tags xiangwan-admin
// @Produce json
// @Param return_to query string false "Local path after successful login"
// @Success 302
// @Failure 400 {object} response.Body
// @Failure 429 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/admin/auth/login [get]
func (handler *AdminAuthHandler) Login(c *gin.Context) {
	if handler == nil || handler.oidc == nil || handler.sessions == nil {
		writeAdminError(c, xiangwanadmin.ErrInvalidAdminConfiguration)
		return
	}
	if len(c.Request.URL.Query()) > 1 ||
		(len(c.Request.URL.Query()) == 1 && !c.Request.URL.Query().Has("return_to")) {
		writeError(c, errx.NewBadRequest("invalid administrator login request"))
		return
	}
	returnTo := c.Query("return_to")
	if returnTo == "" {
		returnTo = "/activities"
	}
	if !xiangwanadmin.ValidReturnTo(returnTo) {
		writeError(c, errx.NewBadRequest("invalid administrator return path"))
		return
	}
	login, err := handler.sessions.BeginLogin(c.Request.Context(), returnTo, c.ClientIP())
	if err != nil {
		writeAdminError(c, err)
		return
	}
	authorizationURL := handler.oidc.AuthorizationURL(
		login.State,
		login.Nonce,
		login.PKCEChallenge,
	)
	if authorizationURL == "" {
		writeAdminError(c, xiangwanadmin.ErrInvalidAdminConfiguration)
		return
	}
	handler.setLoginCookie(c, login)
	c.Redirect(http.StatusFound, authorizationURL)
}

// Callback godoc
// @Summary Complete Xiangwan administrator OIDC login
// @Description Validates state, nonce, PKCE, issuer, audience, optional ACR, exact identity link, Principal, and live Grant before issuing an opaque secure cookie.
// @Tags xiangwan-admin
// @Produce json
// @Param code query string true "OIDC authorization code"
// @Param state query string true "Single-use login state"
// @Success 303
// @Failure 503 {object} response.Body
// @Router /xiangwan/admin/auth/callback [get]
func (handler *AdminAuthHandler) Callback(c *gin.Context) {
	if handler == nil || handler.oidc == nil || handler.sessions == nil {
		writeAdminError(c, xiangwanadmin.ErrInvalidAdminConfiguration)
		return
	}
	browserBinding, bindingErr := c.Cookie(xiangwanadmin.LoginCookieName)
	handler.clearLoginCookie(c)
	if c.Query("error") != "" {
		c.Redirect(http.StatusSeeOther, "/login?reason=identity_rejected")
		return
	}
	code := c.Query("code")
	state := c.Query("state")
	if bindingErr != nil || browserBinding == "" || len(browserBinding) > 256 ||
		code == "" || state == "" || len(code) > 4096 || len(state) > 256 {
		c.Redirect(http.StatusSeeOther, "/login?reason=identity_rejected")
		return
	}
	pending, err := handler.sessions.ClaimLogin(
		c.Request.Context(), state, browserBinding,
	)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/login?reason=login_expired")
		return
	}
	exchangeCtx, cancelExchange := context.WithTimeout(
		c.Request.Context(), adminOIDCExchangeTimeout,
	)
	defer cancelExchange()
	identity, err := handler.oidc.Exchange(
		exchangeCtx,
		code,
		pending.PKCEVerifier,
	)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/login?reason=identity_rejected")
		return
	}
	credentials, err := handler.sessions.FinishLogin(
		c.Request.Context(),
		pending,
		identity,
		adminRequestID(c),
	)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/login?reason=identity_rejected")
		return
	}
	handler.setCookies(c, credentials)
	c.Redirect(http.StatusSeeOther, credentials.ReturnTo)
}

// Logout godoc
// @Summary Revoke the current Xiangwan administrator session
// @Description Requires the opaque administrator cookie plus same-origin double-submit CSRF proof and revokes the PostgreSQL session.
// @Tags xiangwan-admin
// @Produce json
// @Success 200 {object} map[string]bool
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/admin/auth/logout [post]
func (handler *AdminAuthHandler) Logout(c *gin.Context) {
	principal, err := AdminPrincipal(c)
	if err != nil {
		writeAdminError(c, err)
		return
	}
	if err := handler.sessions.RevokeSession(
		c.Request.Context(),
		principal.SessionID,
		principal.PrincipalID,
		adminRequestID(c),
	); err != nil {
		writeAdminError(c, err)
		return
	}
	handler.clearCookies(c)
	response.OK(c, gin.H{"logged_out": true})
}

func (handler *AdminAuthHandler) ResolveOnsiteAdminPrincipal(
	c *gin.Context,
) (xiangwanadmin.Principal, error) {
	principal, err := AdminPrincipal(c)
	if err != nil {
		return xiangwanadmin.Principal{}, err
	}
	return principal, nil
}

func AdminPrincipal(c *gin.Context) (xiangwanadmin.Principal, error) {
	if c == nil {
		return xiangwanadmin.Principal{}, xiangwanadmin.ErrSessionInvalid
	}
	value, exists := c.Get(adminPrincipalContextKey)
	principal, ok := value.(xiangwanadmin.Principal)
	if !exists || !ok || principal.PrincipalID == uuid.Nil {
		return xiangwanadmin.Principal{}, xiangwanadmin.ErrSessionInvalid
	}
	return principal, nil
}

func (handler *AdminAuthHandler) verifyCSRF(
	c *gin.Context,
	principal xiangwanadmin.Principal,
) error {
	if !handler.trustedRequestOrigin(c.Request) {
		return errx.New(
			errx.CodeXiangwanAdminCSRFRejected,
			"administrator request verification failed",
		)
	}
	headerValues := c.Request.Header.Values(xiangwanadmin.CSRFHeaderName)
	csrfCookie, cookieErr := c.Cookie(xiangwanadmin.CSRFCookieName)
	if len(headerValues) != 1 || cookieErr != nil || csrfCookie == "" ||
		headerValues[0] != csrfCookie {
		return errx.New(errx.CodeXiangwanAdminCSRFRejected, "administrator request verification failed")
	}
	digest := sha256.Sum256([]byte(csrfCookie))
	if digest != principal.CSRFTokenHash {
		return errx.New(errx.CodeXiangwanAdminCSRFRejected, "administrator request verification failed")
	}
	return nil
}

func (handler *AdminAuthHandler) trustedRequestOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin != "" {
		normalized, err := normalizeAdminOrigin(origin)
		if err != nil {
			return false
		}
		_, allowed := handler.allowedOrigins[normalized]
		return allowed
	}
	referer := request.Header.Get("Referer")
	parsed, err := url.Parse(referer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	normalized, err := normalizeAdminOrigin(parsed.Scheme + "://" + parsed.Host)
	if err != nil {
		return false
	}
	_, allowed := handler.allowedOrigins[normalized]
	return allowed
}

func (handler *AdminAuthHandler) setCookies(
	c *gin.Context,
	credentials xiangwanadmin.SessionCredentials,
) {
	maxAge := int(time.Until(credentials.Principal.AbsoluteExpiry).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: xiangwanadmin.SessionCookieName, Value: credentials.Token,
		Path: "/", MaxAge: maxAge, Expires: credentials.Principal.AbsoluteExpiry,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name: xiangwanadmin.CSRFCookieName, Value: credentials.CSRFToken,
		Path: "/", MaxAge: maxAge, Expires: credentials.Principal.AbsoluteExpiry,
		Secure: true, HttpOnly: false, SameSite: http.SameSiteLaxMode,
	})
}

func (handler *AdminAuthHandler) setLoginCookie(
	c *gin.Context,
	login xiangwanadmin.LoginStart,
) {
	maxAge := int(time.Until(login.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: xiangwanadmin.LoginCookieName, Value: login.BrowserBinding,
		Path: "/", MaxAge: maxAge, Expires: login.ExpiresAt,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (handler *AdminAuthHandler) clearLoginCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: xiangwanadmin.LoginCookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (handler *AdminAuthHandler) clearCookies(c *gin.Context) {
	for _, cookie := range []struct {
		name     string
		httpOnly bool
	}{
		{name: xiangwanadmin.SessionCookieName, httpOnly: true},
		{name: xiangwanadmin.CSRFCookieName, httpOnly: false},
	} {
		http.SetCookie(c.Writer, &http.Cookie{
			Name: cookie.name, Value: "", Path: "/", MaxAge: -1,
			Expires: time.Unix(1, 0), Secure: true, HttpOnly: cookie.httpOnly,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

type AdminAuthStatusResponse struct {
	Enabled       bool                 `json:"enabled"`
	Authenticated bool                 `json:"authenticated"`
	PrincipalID   string               `json:"principal_id,omitempty"`
	ExpiresAt     string               `json:"expires_at,omitempty"`
	Grants        []AdminGrantResponse `json:"grants"`
}

type AdminGrantResponse struct {
	Capability string  `json:"capability"`
	ScopeType  string  `json:"scope_type"`
	ScopeID    *string `json:"scope_id,omitempty"`
}

func projectAdminGrants(grants []xiangwanadmin.Grant) []AdminGrantResponse {
	result := make([]AdminGrantResponse, 0, len(grants))
	for _, grant := range grants {
		item := AdminGrantResponse{
			Capability: string(grant.Capability),
			ScopeType:  string(grant.ScopeType),
		}
		if grant.ScopeID != nil {
			value := grant.ScopeID.String()
			item.ScopeID = &value
		}
		result = append(result, item)
	}
	return result
}

func writeAdminError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, xiangwanadmin.ErrInvalidAdminConfiguration):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminUnavailable,
			"administrator login is unavailable",
		))
	case errors.Is(err, xiangwanadmin.ErrSessionInvalid),
		errors.Is(err, xiangwanadmin.ErrLoginAttemptUnavailable):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminSessionInvalid,
			"administrator session is invalid",
		))
	case errors.Is(err, xiangwanadmin.ErrScopeForbidden):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminScopeForbidden,
			"administrator scope is forbidden",
		))
	case errors.Is(err, xiangwanadmin.ErrLoginRateLimited):
		c.Header("Retry-After", "60")
		writeError(c, errx.New(
			errx.CodeXiangwanAdminLoginRateLimited,
			"too many administrator login attempts",
		))
	case errors.Is(err, xiangwanadmin.ErrIdentityRejected):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminIdentityRejected,
			"administrator identity is not authorized",
		))
	default:
		writeError(c, err)
	}
}

func adminWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func normalizeAdminOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", xiangwanadmin.ErrInvalidAdminConfiguration
	}
	// Browsers serialize an Origin without the scheme default port, so an
	// explicit :443 in configuration has to collapse to the same value or every
	// same-origin write would fail the CSRF check.
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", xiangwanadmin.ErrInvalidAdminConfiguration
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := parsed.Port(); port != "" {
		portNumber, portErr := strconv.ParseUint(port, 10, 16)
		if portErr != nil {
			return "", xiangwanadmin.ErrInvalidAdminConfiguration
		}
		if portNumber != 443 {
			host += ":" + strconv.FormatUint(portNumber, 10)
		}
	}
	return parsed.Scheme + "://" + host, nil
}

func adminRequestID(c *gin.Context) string {
	if value := requestctx.RequestID(c.Request.Context()); value != "" {
		return value
	}
	if value := c.GetHeader(requestctx.RequestIDHeader); value != "" {
		return value
	}
	return uuid.NewString()
}
