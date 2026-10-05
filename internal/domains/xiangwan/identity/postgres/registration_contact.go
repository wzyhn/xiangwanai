package identitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	"github.com/google/uuid"
)

var (
	ErrInvalidRegistrationContact        = errors.New("invalid registration contact")
	ErrRegistrationContactConflict       = errors.New("registration contact changed")
	ErrRegistrationContactPolicyConflict = errors.New("registration contact policy changed")
	registrationPhonePattern             = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
)

type RegistrationContact struct {
	Configured           bool   `json:"configured"`
	Nickname             string `json:"nickname"`
	PhoneE164            string `json:"phone_e164"`
	PrincipalProfileETag string `json:"principal_profile_etag"`
	Version              int64  `json:"version"`
	PrivacyPolicyVersion string `json:"privacy_policy_version"`
	ContactPolicyVersion string `json:"contact_policy_version"`
}

type RegistrationContactUpdate struct {
	Nickname             string
	PhoneE164            string
	PrincipalProfileETag string
	ExpectedVersion      int64
	PrivacyPolicyVersion string
	ContactPolicyVersion string
}

// RegistrationContactStore is a private, tenant-scoped identity write seam.
// It never changes an existing registration, exposes phone publicly, or treats
// a manually entered phone as verified identity. Logs contain no input values.
type RegistrationContactStore struct {
	database       *sql.DB
	tenantID       uuid.UUID
	generationID   uuid.UUID
	privacyVersion string
	contactVersion string
	enabled        bool
}

func NewRegistrationContactStore(db *sql.DB, tenantID, generationID uuid.UUID,
	privacyVersion, contactVersion string, enabled bool) *RegistrationContactStore {
	return &RegistrationContactStore{db, tenantID, generationID, privacyVersion, contactVersion, enabled}
}

func (store *RegistrationContactStore) Read(ctx context.Context, principalID uuid.UUID) (RegistrationContact, error) {
	if store == nil || store.database == nil || store.tenantID == uuid.Nil || principalID == uuid.Nil {
		return RegistrationContact{}, ErrInvalidRegistrationContact
	}
	var value RegistrationContact
	var avatar string
	var fileID uuid.NullUUID
	err := store.database.QueryRowContext(ctx, `
SELECT p.nickname, p.avatar_url, p.avatar_file_id,
       COALESCE(c.phone_e164, ''), COALESCE(c.version, 0),
       COALESCE(c.privacy_policy_version, ''), COALESCE(c.contact_policy_version, '')
FROM principals p
LEFT JOIN xiangwan_registration_contact_defaults c ON c.tenant_id = $1 AND c.principal_id = p.id
WHERE p.id = $2 AND p.status = 'active' AND p.deleted_at IS NULL
`, store.tenantID, principalID).Scan(&value.Nickname, &avatar, &fileID,
		&value.PhoneE164, &value.Version, &value.PrivacyPolicyVersion, &value.ContactPolicyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistrationContact{}, ErrPrincipalUnavailable
	}
	if err != nil {
		return RegistrationContact{}, errors.New("read registration contact failed")
	}
	value.PrincipalProfileETag, err = contactProfileETag(principalID, value.Nickname, avatar, fileID)
	value.Configured = value.Version > 0
	return value, err
}

// Update uses BUSINESS-STATE compare-and-set semantics. After an unknown
// response the client reads this exact private target; matching target values
// and policies confirm success, otherwise a stale version cannot overwrite it.
func (store *RegistrationContactStore) Update(ctx context.Context, principalID uuid.UUID, input RegistrationContactUpdate) (RegistrationContact, error) {
	if store == nil || store.database == nil || store.tenantID == uuid.Nil ||
		store.generationID == uuid.Nil || principalID == uuid.Nil || ctx == nil ||
		input.Nickname != strings.TrimSpace(input.Nickname) || len([]rune(input.Nickname)) < 1 ||
		len([]rune(input.Nickname)) > 64 || strings.ContainsFunc(input.Nickname, unicode.IsControl) ||
		!registrationPhonePattern.MatchString(input.PhoneE164) || input.PrincipalProfileETag == "" || input.ExpectedVersion < 0 {
		return RegistrationContact{}, ErrInvalidRegistrationContact
	}
	if !store.enabled || store.privacyVersion == "" || store.contactVersion == "" ||
		input.PrivacyPolicyVersion != store.privacyVersion || input.ContactPolicyVersion != store.contactVersion {
		return RegistrationContact{}, ErrRegistrationContactPolicyConflict
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationContact{}, errors.New("begin registration contact failed")
	}
	defer func() { _ = tx.Rollback() }()
	var epoch int64
	err = tx.QueryRowContext(ctx, `
SELECT g.write_epoch FROM xiangwan_runtime_generations g
JOIN tenants t ON t.id = g.tenant_id
WHERE g.singleton_id = 1 AND g.scope_key = 'wq-xiangwan' AND g.tenant_id = $1
  AND g.active_generation_id = $2 AND g.write_epoch > 0 AND g.bootstrap_completed_at IS NOT NULL
  AND t.type = 'business' AND t.metadata @> '{"product_code":"wq-xiangwan"}'::jsonb
FOR SHARE OF g
`, store.tenantID, store.generationID).Scan(&epoch)
	if err != nil {
		return RegistrationContact{}, ErrPrincipalUnavailable
	}
	var nickname, avatar string
	var fileID uuid.NullUUID
	err = tx.QueryRowContext(ctx, `SELECT nickname, avatar_url, avatar_file_id FROM principals
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL FOR UPDATE`, principalID).Scan(&nickname, &avatar, &fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistrationContact{}, ErrPrincipalUnavailable
	}
	if err != nil {
		return RegistrationContact{}, errors.New("lock registration contact account failed")
	}
	etag, err := contactProfileETag(principalID, nickname, avatar, fileID)
	if err != nil {
		return RegistrationContact{}, err
	}
	var existing RegistrationContact
	err = tx.QueryRowContext(ctx, `SELECT phone_e164, version, privacy_policy_version, contact_policy_version
FROM xiangwan_registration_contact_defaults WHERE tenant_id = $1 AND principal_id = $2 FOR UPDATE`,
		store.tenantID, principalID).Scan(&existing.PhoneE164, &existing.Version, &existing.PrivacyPolicyVersion, &existing.ContactPolicyVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RegistrationContact{}, errors.New("lock registration contact failed")
	}
	if existing.Version > 0 && nickname == input.Nickname && existing.PhoneE164 == input.PhoneE164 &&
		existing.PrivacyPolicyVersion == input.PrivacyPolicyVersion && existing.ContactPolicyVersion == input.ContactPolicyVersion {
		existing.Nickname, existing.PrincipalProfileETag, existing.Configured = nickname, etag, true
		if err := tx.Commit(); err != nil {
			return RegistrationContact{}, errors.New("commit registration contact failed")
		}
		return existing, nil
	}
	if etag != input.PrincipalProfileETag || existing.Version != input.ExpectedVersion {
		return RegistrationContact{}, ErrRegistrationContactConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE principals SET nickname = $2, updated_at = clock_timestamp() WHERE id = $1`, principalID, input.Nickname); err != nil {
		return RegistrationContact{}, errors.New("save contact nickname failed")
	}
	nextVersion := existing.Version + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO xiangwan_registration_contact_defaults
    (tenant_id, principal_id, phone_e164, privacy_policy_version, contact_policy_version, version)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, principal_id) DO UPDATE SET phone_e164 = EXCLUDED.phone_e164,
    privacy_policy_version = EXCLUDED.privacy_policy_version, contact_policy_version = EXCLUDED.contact_policy_version,
    version = EXCLUDED.version, updated_at = clock_timestamp()`,
		store.tenantID, principalID, input.PhoneE164, input.PrivacyPolicyVersion, input.ContactPolicyVersion, nextVersion); err != nil {
		return RegistrationContact{}, errors.New("save registration contact failed")
	}
	nextETag, err := contactProfileETag(principalID, input.Nickname, avatar, fileID)
	if err != nil {
		return RegistrationContact{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegistrationContact{}, errors.New("commit registration contact failed")
	}
	return RegistrationContact{true, input.Nickname, input.PhoneE164, nextETag, nextVersion,
		input.PrivacyPolicyVersion, input.ContactPolicyVersion}, nil
}

func contactProfileETag(principalID uuid.UUID, nickname, avatar string, fileID uuid.NullUUID) (string, error) {
	var value *uuid.UUID
	if fileID.Valid {
		value = &fileID.UUID
	}
	return consumerprofile.PrincipalProfileETag(principalID, nickname, avatar, value)
}
