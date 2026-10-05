package postgres

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

const (
	loginAttemptTTL           = 5 * time.Minute
	adminSessionIdleTTL       = 30 * time.Minute
	adminSessionMaxTTL        = 8 * time.Hour
	adminSessionTouchInterval = time.Minute
	loginAttemptRateWindow    = time.Minute
	loginAttemptClientLimit   = 10
	loginAttemptTenantLimit   = 300
	loginAttemptCleanupLimit  = 200
)

type SessionStore struct {
	db             *sql.DB
	tenantID       uuid.UUID
	aead           cipher.AEAD
	fingerprintKey [sha256.Size]byte
	random         io.Reader
	now            func() time.Time
}

func NewSessionStore(
	db *sql.DB,
	tenantID uuid.UUID,
	encryptionKey []byte,
) (*SessionStore, error) {
	if db == nil || tenantID == uuid.Nil || len(encryptionKey) != 32 {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	fingerprintKey := sha256.Sum256(append(
		[]byte("xiangwan-admin-login-fingerprint:"),
		encryptionKey...,
	))
	return &SessionStore{
		db: db, tenantID: tenantID, aead: aead,
		fingerprintKey: fingerprintKey,
		random:         cryptorand.Reader, now: time.Now,
	}, nil
}

func (store *SessionStore) BeginLogin(
	ctx context.Context,
	returnTo string,
	clientKey string,
) (xiangwanadmin.LoginStart, error) {
	if !store.valid(ctx) || !xiangwanadmin.ValidReturnTo(returnTo) ||
		clientKey == "" || len(clientKey) > 512 {
		return xiangwanadmin.LoginStart{}, xiangwanadmin.ErrInvalidLoginAttempt
	}
	state, err := store.randomToken(32)
	if err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("create admin login state: %w", err)
	}
	nonce, err := store.randomToken(32)
	if err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("create admin login nonce: %w", err)
	}
	verifier, err := store.randomToken(32)
	if err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("create admin PKCE verifier: %w", err)
	}
	browserBinding, err := store.randomToken(32)
	if err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("create admin browser binding: %w", err)
	}
	encryptedVerifier, err := store.encrypt([]byte(verifier))
	if err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("protect admin PKCE verifier: %w", err)
	}
	now := store.now().UTC()
	stateHash := sha256.Sum256([]byte(state))
	nonceHash := sha256.Sum256([]byte(nonce))
	browserBindingHash := sha256.Sum256([]byte(browserBinding))
	fingerprintMAC := hmac.New(sha256.New, store.fingerprintKey[:])
	_, _ = fingerprintMAC.Write([]byte(clientKey))
	clientFingerprint := fingerprintMAC.Sum(nil)
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("begin admin login attempt: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.QueryRowContext(ctx, `
SELECT TRUE
FROM (SELECT pg_advisory_xact_lock(hashtextextended($1, 0))) AS login_lock
`, store.tenantID.String()).Scan(&locked); err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("lock admin login throttle: %w", err)
	}
	if !locked {
		return xiangwanadmin.LoginStart{}, xiangwanadmin.ErrLoginRateLimited
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM xiangwan_admin_login_attempts
WHERE id IN (
    SELECT id
    FROM xiangwan_admin_login_attempts
    WHERE tenant_id = $1 AND expires_at <= $2
    ORDER BY expires_at, id
    LIMIT $3
)
`, store.tenantID, now, loginAttemptCleanupLimit); err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("clean admin login attempts: %w", err)
	}
	var tenantAttempts int
	var clientAttempts int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*), COUNT(*) FILTER (WHERE client_fingerprint = $2)
FROM xiangwan_admin_login_attempts
WHERE tenant_id = $1 AND created_at > $3
`, store.tenantID, clientFingerprint, now.Add(-loginAttemptRateWindow)).Scan(
		&tenantAttempts,
		&clientAttempts,
	); err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("count admin login attempts: %w", err)
	}
	if tenantAttempts >= loginAttemptTenantLimit || clientAttempts >= loginAttemptClientLimit {
		return xiangwanadmin.LoginStart{}, xiangwanadmin.ErrLoginRateLimited
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_login_attempts (
    id, tenant_id, state_hash, nonce_hash, encrypted_pkce_verifier,
    browser_binding_hash, client_fingerprint, return_to, created_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
`, uuid.New(), store.tenantID, stateHash[:], nonceHash[:], encryptedVerifier,
		browserBindingHash[:], clientFingerprint, returnTo, now, now.Add(loginAttemptTTL))
	if err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("store admin login attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.LoginStart{}, fmt.Errorf("commit admin login attempt: %w", err)
	}
	challengeHash := sha256.Sum256([]byte(verifier))
	return xiangwanadmin.LoginStart{
		State:          state,
		Nonce:          nonce,
		PKCEChallenge:  base64.RawURLEncoding.EncodeToString(challengeHash[:]),
		BrowserBinding: browserBinding,
		ExpiresAt:      now.Add(loginAttemptTTL),
	}, nil
}

// ClaimLogin atomically claims one browser-bound attempt before any
// provider exchange. Repeated callbacks therefore cannot fan out token/JWKS
// requests with the same state.
func (store *SessionStore) ClaimLogin(
	ctx context.Context,
	state string,
	browserBinding string,
) (xiangwanadmin.PendingLogin, error) {
	if !store.valid(ctx) || state == "" || len(state) > 256 ||
		browserBinding == "" || len(browserBinding) > 256 {
		return xiangwanadmin.PendingLogin{}, xiangwanadmin.ErrLoginAttemptUnavailable
	}
	stateHash := sha256.Sum256([]byte(state))
	now := store.now().UTC()
	var pending xiangwanadmin.PendingLogin
	var nonceHash []byte
	var encryptedVerifier []byte
	browserBindingHash := sha256.Sum256([]byte(browserBinding))
	err := store.db.QueryRowContext(ctx, `
UPDATE xiangwan_admin_login_attempts
SET consumed_at = $3
WHERE tenant_id = $1
  AND state_hash = $2
  AND consumed_at IS NULL
  AND expires_at > $3
  AND browser_binding_hash = $4
RETURNING id, nonce_hash, encrypted_pkce_verifier,
          return_to, expires_at, consumed_at
`, store.tenantID, stateHash[:], now, browserBindingHash[:]).Scan(
		&pending.ID, &nonceHash, &encryptedVerifier,
		&pending.ReturnTo, &pending.ExpiresAt, &pending.ClaimedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.PendingLogin{}, xiangwanadmin.ErrLoginAttemptUnavailable
	}
	if err != nil {
		return xiangwanadmin.PendingLogin{}, fmt.Errorf("claim admin login attempt: %w", err)
	}
	if len(nonceHash) != sha256.Size || pending.ClaimedAt.IsZero() {
		return xiangwanadmin.PendingLogin{}, xiangwanadmin.ErrLoginAttemptUnavailable
	}
	copy(pending.NonceHash[:], nonceHash)
	verifier, err := store.decrypt(encryptedVerifier)
	if err != nil {
		return xiangwanadmin.PendingLogin{}, xiangwanadmin.ErrLoginAttemptUnavailable
	}
	pending.PKCEVerifier = string(verifier)
	return pending, nil
}

func (store *SessionStore) FinishLogin(
	ctx context.Context,
	pending xiangwanadmin.PendingLogin,
	identity xiangwanadmin.VerifiedIdentity,
	requestID string,
) (xiangwanadmin.SessionCredentials, error) {
	if !store.valid(ctx) || pending.ID == uuid.Nil || pending.PKCEVerifier == "" ||
		pending.ClaimedAt.IsZero() ||
		identity.Issuer == "" || identity.Subject == "" || identity.Nonce == "" ||
		requestID == "" || len(requestID) > 128 {
		return xiangwanadmin.SessionCredentials{}, xiangwanadmin.ErrIdentityRejected
	}
	nonceHash := sha256.Sum256([]byte(identity.Nonce))
	if nonceHash != pending.NonceHash {
		return xiangwanadmin.SessionCredentials{}, xiangwanadmin.ErrIdentityRejected
	}
	token, err := store.randomToken(32)
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("create admin session token: %w", err)
	}
	csrfToken, err := store.randomToken(32)
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("create admin CSRF token: %w", err)
	}
	tokenHash := sha256.Sum256([]byte(token))
	csrfHash := sha256.Sum256([]byte(csrfToken))
	now := store.now().UTC()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("begin admin login transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var lockedNonceHash []byte
	var returnTo string
	err = tx.QueryRowContext(ctx, `
UPDATE xiangwan_admin_login_attempts
SET completed_at = $4
WHERE tenant_id = $1
  AND id = $2
  AND consumed_at = $3
  AND completed_at IS NULL
  AND expires_at > $4
RETURNING nonce_hash, return_to
`, store.tenantID, pending.ID, pending.ClaimedAt, now).Scan(&lockedNonceHash, &returnTo)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.SessionCredentials{}, xiangwanadmin.ErrLoginAttemptUnavailable
	}
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("consume claimed admin login attempt: %w", err)
	}
	if len(lockedNonceHash) != sha256.Size ||
		!equalBytes(lockedNonceHash, nonceHash[:]) {
		return xiangwanadmin.SessionCredentials{}, xiangwanadmin.ErrLoginAttemptUnavailable
	}

	var principal xiangwanadmin.Principal
	err = tx.QueryRowContext(ctx, `
SELECT identity_link.id, identity_link.principal_id
FROM xiangwan_admin_identity_links AS identity_link
JOIN principals AS principal ON principal.id = identity_link.principal_id
WHERE identity_link.tenant_id = $1
  AND identity_link.issuer = $2
  AND identity_link.subject = $3
  AND identity_link.link_status = 'active'
  AND principal.status = 'active'
  AND principal.deleted_at IS NULL
  AND principal.primary_tenant_id = $1
FOR UPDATE OF identity_link, principal
`, store.tenantID, identity.Issuer, identity.Subject).Scan(
		&principal.IdentityLinkID, &principal.PrincipalID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.SessionCredentials{}, xiangwanadmin.ErrIdentityRejected
	}
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("resolve admin identity: %w", err)
	}
	principal.Grants, err = loadActiveGrants(ctx, tx, store.tenantID, principal.PrincipalID)
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, err
	}
	if len(principal.Grants) == 0 {
		return xiangwanadmin.SessionCredentials{}, xiangwanadmin.ErrIdentityRejected
	}
	principal.SessionID = uuid.New()
	principal.CSRFTokenHash = csrfHash
	principal.AbsoluteExpiry = now.Add(adminSessionMaxTTL)
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_sessions (
    id, tenant_id, principal_id, identity_link_id, token_hash,
    csrf_token_hash, created_at, last_seen_at, idle_expires_at,
    absolute_expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, $9)
`, principal.SessionID, store.tenantID, principal.PrincipalID,
		principal.IdentityLinkID, tokenHash[:], csrfHash[:], now,
		now.Add(adminSessionIdleTTL), principal.AbsoluteExpiry)
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("create admin session: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'admin.login', 'admin_session', $4, $5, '{}'::JSONB, $6, $6)
`, uuid.New(), store.tenantID, principal.PrincipalID,
		principal.SessionID, requestID, now)
	if err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("audit admin login: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.SessionCredentials{}, fmt.Errorf("commit admin login: %w", err)
	}
	return xiangwanadmin.SessionCredentials{
		Principal: principal, Token: token, CSRFToken: csrfToken,
		ReturnTo: returnTo,
	}, nil
}

func (store *SessionStore) ResolveSession(
	ctx context.Context,
	token string,
) (xiangwanadmin.Principal, error) {
	if !store.valid(ctx) || token == "" || len(token) > 256 {
		return xiangwanadmin.Principal{}, xiangwanadmin.ErrSessionInvalid
	}
	tokenHash := sha256.Sum256([]byte(token))
	now := store.now().UTC()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return xiangwanadmin.Principal{}, fmt.Errorf("begin admin session read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var principal xiangwanadmin.Principal
	var csrfHash []byte
	var lastSeen time.Time
	var idleExpiry time.Time
	err = tx.QueryRowContext(ctx, `
SELECT session.id, session.principal_id, session.identity_link_id,
       session.csrf_token_hash, session.last_seen_at,
       session.idle_expires_at, session.absolute_expires_at
FROM xiangwan_admin_sessions AS session
JOIN xiangwan_admin_identity_links AS identity_link
  ON identity_link.tenant_id = session.tenant_id
 AND identity_link.id = session.identity_link_id
JOIN principals AS principal ON principal.id = session.principal_id
WHERE session.tenant_id = $1
  AND session.token_hash = $2
  AND session.revoked_at IS NULL
  AND session.idle_expires_at > $3
  AND session.absolute_expires_at > $3
  AND identity_link.link_status = 'active'
  AND identity_link.principal_id = session.principal_id
  AND principal.status = 'active'
  AND principal.deleted_at IS NULL
  AND principal.primary_tenant_id = $1
FOR UPDATE OF session
`, store.tenantID, tokenHash[:], now).Scan(
		&principal.SessionID, &principal.PrincipalID, &principal.IdentityLinkID,
		&csrfHash, &lastSeen, &idleExpiry, &principal.AbsoluteExpiry,
	)
	if errors.Is(err, sql.ErrNoRows) || len(csrfHash) != sha256.Size {
		return xiangwanadmin.Principal{}, xiangwanadmin.ErrSessionInvalid
	}
	if err != nil {
		return xiangwanadmin.Principal{}, fmt.Errorf("resolve admin session: %w", err)
	}
	copy(principal.CSRFTokenHash[:], csrfHash)
	principal.Grants, err = loadActiveGrants(ctx, tx, store.tenantID, principal.PrincipalID)
	if err != nil {
		return xiangwanadmin.Principal{}, err
	}
	if len(principal.Grants) == 0 {
		return xiangwanadmin.Principal{}, xiangwanadmin.ErrScopeForbidden
	}
	if now.Sub(lastSeen) >= adminSessionTouchInterval {
		nextIdle := now.Add(adminSessionIdleTTL)
		if nextIdle.After(principal.AbsoluteExpiry) {
			nextIdle = principal.AbsoluteExpiry
		}
		if nextIdle.After(idleExpiry) {
			result, updateErr := tx.ExecContext(ctx, `
UPDATE xiangwan_admin_sessions
SET last_seen_at = $3,
    idle_expires_at = $4,
    version = version + 1
WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
`, store.tenantID, principal.SessionID, now, nextIdle)
			if updateErr != nil {
				return xiangwanadmin.Principal{}, fmt.Errorf("touch admin session: %w", updateErr)
			}
			if changed, changedErr := result.RowsAffected(); changedErr != nil || changed != 1 {
				return xiangwanadmin.Principal{}, xiangwanadmin.ErrSessionInvalid
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.Principal{}, fmt.Errorf("commit admin session read: %w", err)
	}
	return principal, nil
}

func (store *SessionStore) RevokeSession(
	ctx context.Context,
	sessionID uuid.UUID,
	principalID uuid.UUID,
	requestID string,
) error {
	if !store.valid(ctx) || sessionID == uuid.Nil || principalID == uuid.Nil ||
		requestID == "" || len(requestID) > 128 {
		return xiangwanadmin.ErrSessionInvalid
	}
	now := store.now().UTC()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin admin logout: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
UPDATE xiangwan_admin_sessions
SET revoked_at = $4,
    revocation_reason = 'operator_logout',
    version = version + 1
WHERE tenant_id = $1 AND id = $2 AND principal_id = $3 AND revoked_at IS NULL
`, store.tenantID, sessionID, principalID, now)
	if err != nil {
		return fmt.Errorf("revoke admin session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return xiangwanadmin.ErrSessionInvalid
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'admin.logout', 'admin_session', $4, $5, '{}'::JSONB, $6, $6)
`, uuid.New(), store.tenantID, principalID, sessionID, requestID, now)
	if err != nil {
		return fmt.Errorf("audit admin logout: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit admin logout: %w", err)
	}
	return nil
}

func loadActiveGrants(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]xiangwanadmin.Grant, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT capability, scope_type, scope_id
FROM xiangwan_admin_grants
WHERE tenant_id = $1
  AND principal_id = $2
  AND domain_code = 'xiangwan'
  AND grant_status = 'active'
ORDER BY capability, scope_type, scope_id NULLS FIRST, id
`, tenantID, principalID)
	if err != nil {
		return nil, fmt.Errorf("load admin grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	grants := make([]xiangwanadmin.Grant, 0)
	for rows.Next() {
		var grant xiangwanadmin.Grant
		var scopeID uuid.NullUUID
		if err := rows.Scan(&grant.Capability, &grant.ScopeType, &scopeID); err != nil {
			return nil, fmt.Errorf("scan admin grant: %w", err)
		}
		if scopeID.Valid {
			value := scopeID.UUID
			grant.ScopeID = &value
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin grants: %w", err)
	}
	return grants, nil
}

func (store *SessionStore) randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(store.random, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (store *SessionStore) encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, store.aead.NonceSize())
	if _, err := io.ReadFull(store.random, nonce); err != nil {
		return nil, err
	}
	sealed := store.aead.Seal(nil, nonce, plaintext, store.tenantID[:])
	return append(nonce, sealed...), nil
}

func (store *SessionStore) decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) <= store.aead.NonceSize() {
		return nil, xiangwanadmin.ErrLoginAttemptUnavailable
	}
	nonce := ciphertext[:store.aead.NonceSize()]
	return store.aead.Open(nil, nonce, ciphertext[store.aead.NonceSize():], store.tenantID[:])
}

func (store *SessionStore) valid(ctx context.Context) bool {
	return store != nil && store.db != nil && store.tenantID != uuid.Nil &&
		store.aead != nil && store.random != nil && store.now != nil && ctx != nil
}

func equalBytes(left []byte, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
