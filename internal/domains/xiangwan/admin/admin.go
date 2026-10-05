// Package xiangwanadmin defines the independent customer administrator
// identity and authorization contracts. It deliberately shares no consumer
// JWT, Console session, Redis cache, or browser token with other products.
package xiangwanadmin

import (
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	SessionCookieName = "__Host-xiangwan_admin_session"
	CSRFCookieName    = "__Host-xiangwan_admin_csrf"
	LoginCookieName   = "__Host-xiangwan_admin_login"
	CSRFHeaderName    = "X-Xiangwan-CSRF-Token"

	CapabilitySuperAdmin       Capability = "super_admin"
	CapabilityActivityOperator Capability = "activity_operator"
	CapabilityOnsiteCheckin    Capability = "onsite_checkin"
	CapabilityFinance          Capability = "finance"

	ScopeTenant  ScopeType = "tenant"
	ScopeSession ScopeType = "session"
)

var (
	ErrInvalidAdminConfiguration = errors.New("invalid xiangwan administrator configuration")
	ErrInvalidLoginAttempt       = errors.New("invalid xiangwan administrator login attempt")
	ErrLoginAttemptUnavailable   = errors.New("xiangwan administrator login attempt unavailable")
	ErrLoginRateLimited          = errors.New("xiangwan administrator login rate limited")
	ErrIdentityRejected          = errors.New("xiangwan administrator identity rejected")
	ErrSessionInvalid            = errors.New("xiangwan administrator session invalid")
	ErrScopeForbidden            = errors.New("xiangwan administrator scope forbidden")
	ErrInvalidCatalogRequest     = errors.New("invalid xiangwan administrator catalog request")
	ErrOperationConflict         = errors.New("xiangwan administrator operation conflict")
	ErrVersionConflict           = errors.New("xiangwan administrator version conflict")
	ErrTargetNotFound            = errors.New("xiangwan administrator target not found")
	ErrPublicationInvalid        = errors.New("xiangwan administrator publication invalid")
	ErrBrandQuickTagInUse        = errors.New(
		"主题标签仍被已发布的活动期次引用，删除前请先处理这些活动",
	)
	// ErrQuickTagNotPublished means a draft/create or publish command references
	// a code that is not in the tenant's current published BrandProfile
	// vocabulary. It is deliberately distinct from malformed input: operators
	// can resolve it by refreshing the home configuration and retrying.
	ErrQuickTagNotPublished = errors.New(
		"活动标签未在当前首页配置中发布，请先更新首页标签",
	)
	ErrInstanceNotEnded = errors.New(
		"活动尚未结束：至少一场结束且其余场次结束或取消后，才能进入往期活动",
	)
	ErrArchiveNotAllowed = errors.New(
		"当前活动或场次状态不允许归档",
	)
	ErrPeopleProfileUnavailable = errors.New(
		"xiangwan PeopleProfile is not bound to an approved active profile",
	)
	ErrInstanceRoleConflict = errors.New(
		"xiangwan Instance role is already assigned",
	)
)

type Capability string
type ScopeType string

type Grant struct {
	Capability Capability `json:"capability"`
	ScopeType  ScopeType  `json:"scope_type"`
	ScopeID    *uuid.UUID `json:"scope_id,omitempty"`
}

type Principal struct {
	SessionID      uuid.UUID
	PrincipalID    uuid.UUID
	IdentityLinkID uuid.UUID
	CSRFTokenHash  [sha256.Size]byte
	Grants         []Grant
	AbsoluteExpiry time.Time
}

func (principal Principal) HasCapability(capability Capability, scopeID *uuid.UUID) bool {
	for _, grant := range principal.Grants {
		if grant.Capability == CapabilitySuperAdmin && grant.ScopeType == ScopeTenant {
			return true
		}
		if grant.Capability != capability {
			continue
		}
		if grant.ScopeType == ScopeTenant {
			return true
		}
		if grant.ScopeType == ScopeSession && grant.ScopeID != nil && scopeID != nil &&
			*grant.ScopeID == *scopeID {
			return true
		}
	}
	return false
}

type PendingLogin struct {
	ID           uuid.UUID
	PKCEVerifier string
	NonceHash    [sha256.Size]byte
	ReturnTo     string
	ExpiresAt    time.Time
	ClaimedAt    time.Time
}

type LoginStart struct {
	State          string
	Nonce          string
	PKCEChallenge  string
	BrowserBinding string
	ExpiresAt      time.Time
}

type SessionCredentials struct {
	Principal Principal
	Token     string
	CSRFToken string
	ReturnTo  string
}

type VerifiedIdentity struct {
	Issuer  string
	Subject string
	Nonce   string
	ACR     string
}

func ValidReturnTo(value string) bool {
	return strings.HasPrefix(value, "/") &&
		!strings.HasPrefix(value, "//") &&
		!strings.ContainsAny(value, "\\\r\n\x00") &&
		len(value) <= 512
}
