package checkin

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewVerificationAttemptRecordsMinimumKnownDecision(t *testing.T) {
	t.Parallel()

	credential := verificationCredential(t)
	command := validVerificationAttemptCommand(credential)
	value, err := NewVerificationAttempt(command)
	if err != nil {
		t.Fatalf(`NewVerificationAttempt() error = %v`, err)
	}
	if value.ID == uuid.Nil ||
		value.TenantID != credential.TenantID ||
		value.CredentialID == nil || *value.CredentialID != credential.ID ||
		value.CredentialJTI == nil || *value.CredentialJTI != credential.CredentialJTI ||
		value.RegistrationID == nil || *value.RegistrationID != credential.RegistrationID ||
		value.PrincipalID == nil || *value.PrincipalID != credential.PrincipalID ||
		value.CheckinID != nil ||
		value.Decision != VerificationDecisionValid ||
		!value.CreatedAt.Equal(value.OccurredAt) {
		t.Fatalf(`NewVerificationAttempt() = %+v`, value)
	}
	credential.ID = uuid.New()
	if *value.CredentialID == credential.ID {
		t.Fatal(`attempt retained mutable Credential pointer`)
	}
}

func TestNewVerificationAttemptRecordsUnknownWithoutPresentedSecret(t *testing.T) {
	t.Parallel()

	credential := verificationCredential(t)
	command := validVerificationAttemptCommand(credential)
	command.CredentialMatched = false
	command.RegistrationEligible = false
	command.Credential = nil
	value, err := NewVerificationAttempt(command)
	if err != nil {
		t.Fatalf(`NewVerificationAttempt() error = %v`, err)
	}
	if value.CredentialID != nil ||
		value.CredentialJTI != nil ||
		value.RegistrationID != nil ||
		value.PrincipalID != nil ||
		value.CheckinID != nil {
		t.Fatalf(`unknown attempt retained credential data = %+v`, value)
	}
}

func TestFingerprintVerificationRequestBindsSecretAndExactContext(t *testing.T) {
	t.Parallel()

	protector := mustCredentialProtector(t, bytesReaderForVerification())
	command := VerificationRequestFingerprintCommand{
		TenantID:            uuid.New(),
		RequestedSeriesID:   uuid.New(),
		RequestedInstanceID: uuid.New(),
		RequestedSessionID:  uuid.New(),
		ActorID:             uuid.New(),
		PresentedKind:       PresentedCredentialKindBackupCode,
		PresentedValue:      `ABCD-EFGH-JKMN`,
	}
	first, err := protector.FingerprintVerificationRequest(command)
	if err != nil {
		t.Fatalf(`FingerprintVerificationRequest() error = %v`, err)
	}
	replayed, err := protector.FingerprintVerificationRequest(command)
	if err != nil || replayed != first || first == (CredentialDigest{}) {
		t.Fatalf(`replayed fingerprint = %x, %v`, replayed, err)
	}
	command.PresentedValue = `ABCD-EFGH-JKMP`
	differentSecret, err := protector.FingerprintVerificationRequest(command)
	if err != nil || differentSecret == first {
		t.Fatalf(`different-secret fingerprint = %x, %v`, differentSecret, err)
	}
	command.PresentedValue = `ABCD-EFGH-JKMN`
	command.RequestedSessionID = uuid.New()
	differentContext, err := protector.FingerprintVerificationRequest(command)
	if err != nil || differentContext == first {
		t.Fatalf(`different-context fingerprint = %x, %v`, differentContext, err)
	}
}

func TestNewVerificationAttemptDerivesDecisionFromTrustedFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*NewVerificationAttemptCommand, *Credential)
		want   VerificationDecision
	}{
		{
			name: `valid`,
			want: VerificationDecisionValid,
		},
		{
			name: `wrong context`,
			mutate: func(command *NewVerificationAttemptCommand, _ *Credential) {
				command.RequestedSessionID = uuid.New()
			},
			want: VerificationDecisionWrongContext,
		},
		{
			name: `revoked`,
			mutate: func(command *NewVerificationAttemptCommand, credential *Credential) {
				reason := `rotation`
				revokedAt := credential.IssuedAt.Add(30 * time.Second)
				credential.CredentialStatus = CredentialStatusRevoked
				credential.RevokedAt = &revokedAt
				credential.RevocationReason = &reason
				credential.Version = 2
				credential.UpdatedAt = revokedAt
				command.Credential = credential
			},
			want: VerificationDecisionRevoked,
		},
		{
			name: `expired boundary`,
			mutate: func(command *NewVerificationAttemptCommand, credential *Credential) {
				command.At = credential.ExpiresAt
			},
			want: VerificationDecisionExpired,
		},
		{
			name: `Registration ineligible`,
			mutate: func(command *NewVerificationAttemptCommand, _ *Credential) {
				command.RegistrationEligible = false
			},
			want: VerificationDecisionRegistrationIneligible,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			credential := verificationCredential(t)
			command := validVerificationAttemptCommand(credential)
			if test.mutate != nil {
				test.mutate(&command, &credential)
			}
			attempt, err := NewVerificationAttempt(command)
			if err != nil {
				t.Fatalf(`NewVerificationAttempt() error = %v`, err)
			}
			if attempt.Decision != test.want {
				t.Fatalf(`Decision = %q, want %q`, attempt.Decision, test.want)
			}
		})
	}
}

func TestNewVerificationAttemptBindsAlreadyCheckedInFact(t *testing.T) {
	t.Parallel()

	credential := verificationCredential(t)
	checkinValue, err := New(NewCommand{
		TenantID:       credential.TenantID,
		RegistrationID: credential.RegistrationID,
		SeriesID:       credential.SeriesID,
		InstanceID:     credential.InstanceID,
		SessionID:      credential.SessionID,
		PrincipalID:    credential.PrincipalID,
		CheckedInBy:    uuid.New(),
		At:             credential.IssuedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`New(Checkin) error = %v`, err)
	}
	command := validVerificationAttemptCommand(credential)
	command.Checkin = &checkinValue
	value, err := NewVerificationAttempt(command)
	if err != nil {
		t.Fatalf(`NewVerificationAttempt() error = %v`, err)
	}
	if value.CheckinID == nil || *value.CheckinID != checkinValue.ID {
		t.Fatalf(`CheckinID = %v, want %s`, value.CheckinID, checkinValue.ID)
	}
}

func TestNewVerificationAttemptRejectsImpossibleShapes(t *testing.T) {
	t.Parallel()

	credential := verificationCredential(t)
	base := validVerificationAttemptCommand(credential)
	tests := []struct {
		name   string
		mutate func(*NewVerificationAttemptCommand)
	}{
		{`tenant`, func(value *NewVerificationAttemptCommand) { value.TenantID = uuid.Nil }},
		{`actor`, func(value *NewVerificationAttemptCommand) { value.ActorID = uuid.Nil }},
		{`kind`, func(value *NewVerificationAttemptCommand) { value.PresentedKind = PresentedCredentialKind(`raw`) }},
		{`idempotency`, func(value *NewVerificationAttemptCommand) { value.IdempotencyKey = `bad key` }},
		{`request fingerprint`, func(value *NewVerificationAttemptCommand) {
			value.RequestFingerprint = CredentialDigest{}
		}},
		{`known missing Credential`, func(value *NewVerificationAttemptCommand) { value.Credential = nil }},
		{`unmatched with Credential`, func(value *NewVerificationAttemptCommand) { value.CredentialMatched = false }},
		{`ineligible with Checkin`, func(value *NewVerificationAttemptCommand) {
			value.RegistrationEligible = false
			value.Checkin = pointerToCheckin(t, credential)
		}},
		{`cross-tenant Credential`, func(value *NewVerificationAttemptCommand) { value.TenantID = uuid.New() }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := base
			test.mutate(&command)
			if _, err := NewVerificationAttempt(command); !errors.Is(
				err,
				ErrInvalidVerificationAttempt,
			) {
				t.Fatalf(`NewVerificationAttempt() error = %v`, err)
			}
		})
	}
}

func verificationCredential(t *testing.T) Credential {
	t.Helper()
	protector := mustCredentialProtector(t, bytesReaderForVerification())
	issued, err := protector.Issue(validIssueCredentialCommand(time.Now().UTC()))
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	return issued.Credential
}

func validVerificationAttemptCommand(
	credential Credential,
) NewVerificationAttemptCommand {
	fingerprint := CredentialDigest{0xA7}
	return NewVerificationAttemptCommand{
		TenantID:             credential.TenantID,
		RequestedSeriesID:    credential.SeriesID,
		RequestedInstanceID:  credential.InstanceID,
		RequestedSessionID:   credential.SessionID,
		ActorID:              uuid.New(),
		PresentedKind:        PresentedCredentialKindQRToken,
		IdempotencyKey:       `verify:001`,
		RequestFingerprint:   fingerprint,
		CredentialMatched:    true,
		RegistrationEligible: true,
		Credential:           &credential,
		At:                   credential.IssuedAt.Add(time.Minute),
	}
}

func pointerToCheckin(t *testing.T, credential Credential) *Checkin {
	t.Helper()
	value, err := New(NewCommand{
		TenantID:       credential.TenantID,
		RegistrationID: credential.RegistrationID,
		SeriesID:       credential.SeriesID,
		InstanceID:     credential.InstanceID,
		SessionID:      credential.SessionID,
		PrincipalID:    credential.PrincipalID,
		CheckedInBy:    uuid.New(),
		At:             credential.IssuedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`New(Checkin) error = %v`, err)
	}
	return &value
}

func bytesReaderForVerification() *repeatingReader {
	return &repeatingReader{value: 0x6A}
}

type repeatingReader struct {
	value byte
}

func (reader *repeatingReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = reader.value
	}
	reader.value++
	return len(destination), nil
}
