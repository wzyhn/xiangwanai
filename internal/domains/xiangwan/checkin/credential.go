package checkin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	MaxCredentialTTL = 15 * time.Minute
	backupCodeLength = 12
	qrSecretLength   = 32
)

const backupCodeAlphabet = `0123456789ABCDEFGHJKMNPQRSTVWXYZ`

type CredentialStatus string

const (
	CredentialStatusActive  CredentialStatus = `active`
	CredentialStatusRevoked CredentialStatus = `revoked`
)

type CredentialDigest [sha256.Size]byte

type Credential struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	RegistrationID   uuid.UUID
	SeriesID         uuid.UUID
	InstanceID       uuid.UUID
	SessionID        uuid.UUID
	PrincipalID      uuid.UUID
	CredentialJTI    uuid.UUID
	QRTokenHash      CredentialDigest
	BackupCodeHash   CredentialDigest
	CredentialEpoch  int64
	CredentialStatus CredentialStatus
	IssuedAt         time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
	RevocationReason *string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type IssueCredentialCommand struct {
	TenantID       uuid.UUID
	RegistrationID uuid.UUID
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	PrincipalID    uuid.UUID
	Epoch          int64
	TTL            time.Duration
	At             time.Time
}

type IssuedCredential struct {
	Credential Credential
	QRToken    string
	BackupCode string
}

func (IssuedCredential) String() string {
	return "xiangwan IssuedCredential{secrets:[REDACTED]}"
}

func (value IssuedCredential) GoString() string {
	return value.String()
}

var (
	ErrInvalidCredential       = errors.New(`invalid xiangwan Checkin credential`)
	ErrCredentialTerminal      = errors.New(`xiangwan Checkin credential is terminal`)
	ErrInvalidCredentialKey    = errors.New(`invalid xiangwan Checkin credential key`)
	ErrInvalidPresentedQRToken = errors.New(`invalid xiangwan Checkin QR token`)
	ErrInvalidBackupCode       = errors.New(`invalid xiangwan Checkin backup code`)
	ErrCredentialEntropy       = errors.New(`generate xiangwan Checkin credential entropy`)
)

type CredentialProtector struct {
	hmacKey    []byte
	randomness io.Reader
}

func NewCredentialProtector(
	hmacKey []byte,
	randomness io.Reader,
) (*CredentialProtector, error) {
	if len(hmacKey) < sha256.Size || randomness == nil {
		return nil, ErrInvalidCredentialKey
	}
	return &CredentialProtector{
		hmacKey:    append([]byte(nil), hmacKey...),
		randomness: randomness,
	}, nil
}

func (protector *CredentialProtector) Issue(
	command IssueCredentialCommand,
) (IssuedCredential, error) {
	if protector == nil ||
		len(protector.hmacKey) < sha256.Size ||
		protector.randomness == nil ||
		command.TenantID == uuid.Nil ||
		command.RegistrationID == uuid.Nil ||
		command.SeriesID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.SessionID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		command.Epoch < 1 ||
		command.TTL <= 0 ||
		command.TTL > MaxCredentialTTL ||
		command.At.IsZero() {
		return IssuedCredential{}, ErrInvalidCredential
	}

	credentialID, err := uuid.NewRandomFromReader(protector.randomness)
	if err != nil {
		return IssuedCredential{}, fmt.Errorf(`%w: credential id: %v`, ErrCredentialEntropy, err)
	}
	jti, err := uuid.NewRandomFromReader(protector.randomness)
	if err != nil {
		return IssuedCredential{}, fmt.Errorf(`%w: jti: %v`, ErrCredentialEntropy, err)
	}
	qrSecret := make([]byte, qrSecretLength)
	if _, err := io.ReadFull(protector.randomness, qrSecret); err != nil {
		return IssuedCredential{}, fmt.Errorf(`%w: QR token: %v`, ErrCredentialEntropy, err)
	}
	backupEntropy := make([]byte, backupCodeLength)
	if _, err := io.ReadFull(protector.randomness, backupEntropy); err != nil {
		return IssuedCredential{}, fmt.Errorf(`%w: backup code: %v`, ErrCredentialEntropy, err)
	}

	qrToken := `xw1.` + jti.String() + `.` +
		base64.RawURLEncoding.EncodeToString(qrSecret)
	backupCanonical := make([]byte, backupCodeLength)
	for index, value := range backupEntropy {
		backupCanonical[index] = backupCodeAlphabet[int(value)&31]
	}
	backupCode := formatBackupCode(string(backupCanonical))
	issuedAt := command.At.UTC()
	credential := Credential{
		ID:               credentialID,
		TenantID:         command.TenantID,
		RegistrationID:   command.RegistrationID,
		SeriesID:         command.SeriesID,
		InstanceID:       command.InstanceID,
		SessionID:        command.SessionID,
		PrincipalID:      command.PrincipalID,
		CredentialJTI:    jti,
		QRTokenHash:      protector.digest(`qr:` + qrToken),
		BackupCodeHash:   protector.digest(`backup:` + string(backupCanonical)),
		CredentialEpoch:  command.Epoch,
		CredentialStatus: CredentialStatusActive,
		IssuedAt:         issuedAt,
		ExpiresAt:        issuedAt.Add(command.TTL),
		Version:          1,
		CreatedAt:        issuedAt,
		UpdatedAt:        issuedAt,
	}
	if err := ValidateCredential(credential); err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{
		Credential: credential,
		QRToken:    qrToken,
		BackupCode: backupCode,
	}, nil
}

func (protector *CredentialProtector) HashQRToken(
	presented string,
) (uuid.UUID, CredentialDigest, error) {
	if protector == nil || len(protector.hmacKey) < sha256.Size {
		return uuid.Nil, CredentialDigest{}, ErrInvalidCredentialKey
	}
	parts := strings.Split(presented, `.`)
	if len(parts) != 3 || parts[0] != `xw1` {
		return uuid.Nil, CredentialDigest{}, ErrInvalidPresentedQRToken
	}
	jti, err := uuid.Parse(parts[1])
	if err != nil || jti == uuid.Nil || jti.String() != parts[1] {
		return uuid.Nil, CredentialDigest{}, ErrInvalidPresentedQRToken
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil ||
		len(secret) != qrSecretLength ||
		base64.RawURLEncoding.EncodeToString(secret) != parts[2] {
		return uuid.Nil, CredentialDigest{}, ErrInvalidPresentedQRToken
	}
	return jti, protector.digest(`qr:` + presented), nil
}

func (protector *CredentialProtector) HashBackupCode(
	presented string,
) (CredentialDigest, error) {
	if protector == nil || len(protector.hmacKey) < sha256.Size {
		return CredentialDigest{}, ErrInvalidCredentialKey
	}
	canonical, err := normalizeBackupCode(presented)
	if err != nil {
		return CredentialDigest{}, err
	}
	return protector.digest(`backup:` + canonical), nil
}

func (protector *CredentialProtector) MatchesQRToken(
	credential Credential,
	presented string,
) bool {
	jti, digest, err := protector.HashQRToken(presented)
	return err == nil &&
		jti == credential.CredentialJTI &&
		hmac.Equal(digest[:], credential.QRTokenHash[:])
}

func (protector *CredentialProtector) MatchesBackupCode(
	credential Credential,
	presented string,
) bool {
	digest, err := protector.HashBackupCode(presented)
	return err == nil && hmac.Equal(digest[:], credential.BackupCodeHash[:])
}

func RevokeCredential(
	current Credential,
	reason string,
	at time.Time,
) (Credential, error) {
	if err := ValidateCredential(current); err != nil {
		return Credential{}, err
	}
	if current.CredentialStatus == CredentialStatusRevoked {
		return Credential{}, ErrCredentialTerminal
	}
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == `` ||
		len([]rune(normalizedReason)) > 500 ||
		at.IsZero() ||
		at.Before(current.UpdatedAt) {
		return Credential{}, ErrInvalidCredential
	}
	revokedAt := at.UTC()
	result := current
	result.CredentialStatus = CredentialStatusRevoked
	result.RevokedAt = &revokedAt
	result.RevocationReason = &normalizedReason
	result.Version = 2
	result.UpdatedAt = revokedAt
	return result, nil
}

func ValidateCredential(value Credential) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.RegistrationID == uuid.Nil ||
		value.SeriesID == uuid.Nil ||
		value.InstanceID == uuid.Nil ||
		value.SessionID == uuid.Nil ||
		value.PrincipalID == uuid.Nil ||
		value.CredentialJTI == uuid.Nil ||
		value.QRTokenHash == (CredentialDigest{}) ||
		value.BackupCodeHash == (CredentialDigest{}) ||
		value.QRTokenHash == value.BackupCodeHash ||
		value.CredentialEpoch < 1 ||
		value.IssuedAt.IsZero() ||
		value.ExpiresAt.IsZero() ||
		!value.CreatedAt.Equal(value.IssuedAt) ||
		!value.ExpiresAt.After(value.IssuedAt) ||
		value.ExpiresAt.Sub(value.IssuedAt) > MaxCredentialTTL ||
		value.UpdatedAt.Before(value.CreatedAt) {
		return ErrInvalidCredential
	}
	switch value.CredentialStatus {
	case CredentialStatusActive:
		if value.Version != 1 ||
			value.RevokedAt != nil ||
			value.RevocationReason != nil ||
			!value.UpdatedAt.Equal(value.IssuedAt) {
			return ErrInvalidCredential
		}
	case CredentialStatusRevoked:
		if value.Version != 2 ||
			value.RevokedAt == nil ||
			value.RevocationReason == nil ||
			*value.RevocationReason != strings.TrimSpace(*value.RevocationReason) ||
			*value.RevocationReason == `` ||
			len([]rune(*value.RevocationReason)) > 500 ||
			value.RevokedAt.Before(value.IssuedAt) ||
			!value.UpdatedAt.Equal(*value.RevokedAt) {
			return ErrInvalidCredential
		}
	default:
		return ErrInvalidCredential
	}
	return nil
}

func (value Credential) Expired(at time.Time) bool {
	return !at.Before(value.ExpiresAt)
}

func (protector *CredentialProtector) digest(value string) CredentialDigest {
	hasher := hmac.New(sha256.New, protector.hmacKey)
	_, _ = hasher.Write([]byte(value))
	var digest CredentialDigest
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func normalizeBackupCode(value string) (string, error) {
	var builder strings.Builder
	for _, character := range strings.ToUpper(value) {
		if character == '-' || unicode.IsSpace(character) {
			continue
		}
		if !strings.ContainsRune(backupCodeAlphabet, character) {
			return ``, ErrInvalidBackupCode
		}
		builder.WriteRune(character)
	}
	if builder.Len() != backupCodeLength {
		return ``, ErrInvalidBackupCode
	}
	return builder.String(), nil
}

func formatBackupCode(value string) string {
	return value[0:4] + `-` + value[4:8] + `-` + value[8:12]
}
