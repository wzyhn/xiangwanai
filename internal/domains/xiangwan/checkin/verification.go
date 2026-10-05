package checkin

import (
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"
)

const MaxPresentedCredentialLength = 512

type PresentedCredentialKind string

const (
	PresentedCredentialKindQRToken    PresentedCredentialKind = `qr_token`
	PresentedCredentialKindBackupCode PresentedCredentialKind = `backup_code`
)

type VerificationDecision string

const (
	VerificationDecisionValid                  VerificationDecision = `valid`
	VerificationDecisionAlreadyCheckedIn       VerificationDecision = `already_checked_in`
	VerificationDecisionInvalidCredential      VerificationDecision = `invalid_credential`
	VerificationDecisionExpired                VerificationDecision = `expired`
	VerificationDecisionRevoked                VerificationDecision = `revoked`
	VerificationDecisionRegistrationIneligible VerificationDecision = `registration_ineligible`
	VerificationDecisionWrongContext           VerificationDecision = `wrong_context`
)

type VerificationAttempt struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	RequestedSeriesID   uuid.UUID
	RequestedInstanceID uuid.UUID
	RequestedSessionID  uuid.UUID
	ActorID             uuid.UUID
	PresentedKind       PresentedCredentialKind
	Decision            VerificationDecision
	IdempotencyKey      string
	RequestFingerprint  CredentialDigest
	CredentialID        *uuid.UUID
	CredentialJTI       *uuid.UUID
	RegistrationID      *uuid.UUID
	PrincipalID         *uuid.UUID
	CheckinID           *uuid.UUID
	OccurredAt          time.Time
	CreatedAt           time.Time
}

type NewVerificationAttemptCommand struct {
	TenantID             uuid.UUID
	RequestedSeriesID    uuid.UUID
	RequestedInstanceID  uuid.UUID
	RequestedSessionID   uuid.UUID
	ActorID              uuid.UUID
	PresentedKind        PresentedCredentialKind
	IdempotencyKey       string
	RequestFingerprint   CredentialDigest
	CredentialMatched    bool
	RegistrationEligible bool
	Credential           *Credential
	Checkin              *Checkin
	At                   time.Time
}

var ErrInvalidVerificationAttempt = errors.New(
	`invalid xiangwan Checkin verification attempt`,
)

func NewVerificationAttempt(
	command NewVerificationAttemptCommand,
) (VerificationAttempt, error) {
	value := VerificationAttempt{
		ID:                  uuid.New(),
		TenantID:            command.TenantID,
		RequestedSeriesID:   command.RequestedSeriesID,
		RequestedInstanceID: command.RequestedInstanceID,
		RequestedSessionID:  command.RequestedSessionID,
		ActorID:             command.ActorID,
		PresentedKind:       command.PresentedKind,
		IdempotencyKey:      command.IdempotencyKey,
		RequestFingerprint:  command.RequestFingerprint,
		OccurredAt:          command.At.UTC(),
		CreatedAt:           command.At.UTC(),
	}
	decision, err := decideVerification(command)
	if err != nil {
		return VerificationAttempt{}, err
	}
	value.Decision = decision
	if command.CredentialMatched && command.Credential != nil {
		value.CredentialID = cloneUUID(&command.Credential.ID)
		value.CredentialJTI = cloneUUID(&command.Credential.CredentialJTI)
		value.RegistrationID = cloneUUID(&command.Credential.RegistrationID)
		value.PrincipalID = cloneUUID(&command.Credential.PrincipalID)
	}
	if decision == VerificationDecisionAlreadyCheckedIn && command.Checkin != nil {
		value.CheckinID = cloneUUID(&command.Checkin.ID)
	}
	if err := ValidateVerificationAttempt(value); err != nil {
		return VerificationAttempt{}, err
	}
	return value, nil
}

func decideVerification(
	command NewVerificationAttemptCommand,
) (VerificationDecision, error) {
	if !command.CredentialMatched {
		if command.Credential != nil ||
			command.Checkin != nil ||
			command.RegistrationEligible {
			return ``, ErrInvalidVerificationAttempt
		}
		return VerificationDecisionInvalidCredential, nil
	}
	if command.Credential == nil ||
		ValidateCredential(*command.Credential) != nil ||
		command.Credential.TenantID != command.TenantID {
		return ``, ErrInvalidVerificationAttempt
	}
	credential := *command.Credential
	if credential.SeriesID != command.RequestedSeriesID ||
		credential.InstanceID != command.RequestedInstanceID ||
		credential.SessionID != command.RequestedSessionID {
		if command.Checkin != nil {
			return ``, ErrInvalidVerificationAttempt
		}
		return VerificationDecisionWrongContext, nil
	}
	if credential.CredentialStatus == CredentialStatusRevoked {
		if command.Checkin != nil {
			return ``, ErrInvalidVerificationAttempt
		}
		return VerificationDecisionRevoked, nil
	}
	if credential.Expired(command.At) {
		if command.Checkin != nil {
			return ``, ErrInvalidVerificationAttempt
		}
		return VerificationDecisionExpired, nil
	}
	if !command.RegistrationEligible {
		if command.Checkin != nil {
			return ``, ErrInvalidVerificationAttempt
		}
		return VerificationDecisionRegistrationIneligible, nil
	}
	if command.Checkin == nil {
		return VerificationDecisionValid, nil
	}
	if Validate(*command.Checkin) != nil ||
		command.Checkin.TenantID != command.TenantID ||
		command.Checkin.RegistrationID != credential.RegistrationID ||
		command.Checkin.SessionID != credential.SessionID ||
		command.Checkin.PrincipalID != credential.PrincipalID {
		return ``, ErrInvalidVerificationAttempt
	}
	return VerificationDecisionAlreadyCheckedIn, nil
}

func ValidateVerificationAttempt(value VerificationAttempt) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.RequestedSeriesID == uuid.Nil ||
		value.RequestedInstanceID == uuid.Nil ||
		value.RequestedSessionID == uuid.Nil ||
		value.ActorID == uuid.Nil ||
		!validPresentedKind(value.PresentedKind) ||
		!validVerificationDecision(value.Decision) ||
		!idempotencyKeyPattern.MatchString(value.IdempotencyKey) ||
		value.RequestFingerprint == (CredentialDigest{}) ||
		value.OccurredAt.IsZero() ||
		!value.CreatedAt.Equal(value.OccurredAt) {
		return ErrInvalidVerificationAttempt
	}
	known := value.CredentialID != nil &&
		value.CredentialJTI != nil &&
		value.RegistrationID != nil &&
		value.PrincipalID != nil &&
		*value.CredentialID != uuid.Nil &&
		*value.CredentialJTI != uuid.Nil &&
		*value.RegistrationID != uuid.Nil &&
		*value.PrincipalID != uuid.Nil
	allUnknown := value.CredentialID == nil &&
		value.CredentialJTI == nil &&
		value.RegistrationID == nil &&
		value.PrincipalID == nil
	if value.Decision == VerificationDecisionInvalidCredential {
		if !allUnknown || value.CheckinID != nil {
			return ErrInvalidVerificationAttempt
		}
		return nil
	}
	if !known {
		return ErrInvalidVerificationAttempt
	}
	if value.Decision == VerificationDecisionAlreadyCheckedIn {
		if value.CheckinID == nil || *value.CheckinID == uuid.Nil {
			return ErrInvalidVerificationAttempt
		}
	} else if value.CheckinID != nil {
		return ErrInvalidVerificationAttempt
	}
	return nil
}

type VerificationRequestFingerprintCommand struct {
	TenantID            uuid.UUID
	RequestedSeriesID   uuid.UUID
	RequestedInstanceID uuid.UUID
	RequestedSessionID  uuid.UUID
	ActorID             uuid.UUID
	PresentedKind       PresentedCredentialKind
	PresentedValue      string
}

func (protector *CredentialProtector) FingerprintVerificationRequest(
	command VerificationRequestFingerprintCommand,
) (CredentialDigest, error) {
	if protector == nil ||
		len(protector.hmacKey) < sha256.Size ||
		command.TenantID == uuid.Nil ||
		command.RequestedSeriesID == uuid.Nil ||
		command.RequestedInstanceID == uuid.Nil ||
		command.RequestedSessionID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		!validPresentedKind(command.PresentedKind) ||
		command.PresentedValue == "" ||
		len(command.PresentedValue) > MaxPresentedCredentialLength {
		return CredentialDigest{}, ErrInvalidVerificationAttempt
	}
	payload := "verification-request:v1:" +
		command.TenantID.String() + ":" +
		command.RequestedSeriesID.String() + ":" +
		command.RequestedInstanceID.String() + ":" +
		command.RequestedSessionID.String() + ":" +
		command.ActorID.String() + ":" +
		string(command.PresentedKind) + ":" +
		command.PresentedValue
	return protector.digest(payload), nil
}

func validPresentedKind(value PresentedCredentialKind) bool {
	switch value {
	case PresentedCredentialKindQRToken,
		PresentedCredentialKindBackupCode:
		return true
	default:
		return false
	}
}

func validVerificationDecision(value VerificationDecision) bool {
	switch value {
	case VerificationDecisionValid,
		VerificationDecisionAlreadyCheckedIn,
		VerificationDecisionInvalidCredential,
		VerificationDecisionExpired,
		VerificationDecisionRevoked,
		VerificationDecisionRegistrationIneligible,
		VerificationDecisionWrongContext:
		return true
	default:
		return false
	}
}
