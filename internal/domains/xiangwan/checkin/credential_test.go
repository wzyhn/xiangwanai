package checkin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCredentialProtectorIssuesOpaqueShortLivedPair(t *testing.T) {
	t.Parallel()

	protector := mustCredentialProtector(t, bytes.NewReader(sequentialEntropy(96)))
	at := time.Date(2026, time.September, 20, 18, 0, 0, 0, time.FixedZone(`CST`, 8*60*60))
	command := validIssueCredentialCommand(at)

	issued, err := protector.Issue(command)
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	credential := issued.Credential
	if credential.ID == uuid.Nil ||
		credential.CredentialJTI == uuid.Nil ||
		credential.TenantID != command.TenantID ||
		credential.RegistrationID != command.RegistrationID ||
		credential.SeriesID != command.SeriesID ||
		credential.InstanceID != command.InstanceID ||
		credential.SessionID != command.SessionID ||
		credential.PrincipalID != command.PrincipalID ||
		credential.CredentialEpoch != command.Epoch ||
		credential.CredentialStatus != CredentialStatusActive ||
		credential.Version != 1 ||
		credential.IssuedAt.Location() != time.UTC ||
		!credential.ExpiresAt.Equal(credential.IssuedAt.Add(command.TTL)) ||
		issued.QRToken == `` ||
		len(strings.ReplaceAll(issued.BackupCode, `-`, ``)) != backupCodeLength {
		t.Fatalf(`Issue() = %+v`, issued)
	}
	if strings.Contains(issued.QRToken, credential.ID.String()) {
		t.Fatal(`QR token exposed credential database primary key`)
	}
	formatted := fmt.Sprintf(`%+v %#v`, issued, issued)
	if strings.Contains(formatted, issued.QRToken) ||
		strings.Contains(formatted, issued.BackupCode) {
		t.Fatal(`formatted issued credential exposed one-time plaintext`)
	}
	if !protector.MatchesQRToken(credential, issued.QRToken) ||
		!protector.MatchesBackupCode(credential, issued.BackupCode) {
		t.Fatal(`issued plaintext does not match stored keyed digests`)
	}
	if err := ValidateCredential(credential); err != nil {
		t.Fatalf(`ValidateCredential() error = %v`, err)
	}
}

func TestCredentialProtectorNormalizesBackupButRejectsTampering(t *testing.T) {
	t.Parallel()

	protector := mustCredentialProtector(t, bytes.NewReader(sequentialEntropy(96)))
	issued, err := protector.Issue(validIssueCredentialCommand(time.Now().UTC()))
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	compact := strings.ToLower(strings.ReplaceAll(issued.BackupCode, `-`, ` `))
	if !protector.MatchesBackupCode(issued.Credential, compact) {
		t.Fatal(`normalized backup code did not match`)
	}
	tamperedCode := `Z` + issued.BackupCode[1:]
	if tamperedCode == issued.BackupCode {
		tamperedCode = `Y` + issued.BackupCode[1:]
	}
	if protector.MatchesBackupCode(issued.Credential, tamperedCode) {
		t.Fatal(`tampered backup code matched`)
	}
	tamperedToken := issued.QRToken[:len(issued.QRToken)-1] + `A`
	if tamperedToken == issued.QRToken {
		tamperedToken = issued.QRToken[:len(issued.QRToken)-1] + `B`
	}
	if protector.MatchesQRToken(issued.Credential, tamperedToken) {
		t.Fatal(`tampered QR token matched`)
	}
	if _, _, err := protector.HashQRToken(`xw1.not-a-jti.value`); !errors.Is(
		err,
		ErrInvalidPresentedQRToken,
	) {
		t.Fatalf(`HashQRToken() error = %v`, err)
	}
	if _, err := protector.HashBackupCode(`IIII-IIII-IIII`); !errors.Is(
		err,
		ErrInvalidBackupCode,
	) {
		t.Fatalf(`HashBackupCode() error = %v`, err)
	}
}

func TestCredentialProtectorRejectsWeakKeyAndInvalidIssue(t *testing.T) {
	t.Parallel()

	if _, err := NewCredentialProtector([]byte(`short`), bytes.NewReader(nil)); !errors.Is(
		err,
		ErrInvalidCredentialKey,
	) {
		t.Fatalf(`NewCredentialProtector() error = %v`, err)
	}
	protector := mustCredentialProtector(t, bytes.NewReader(sequentialEntropy(512)))
	base := validIssueCredentialCommand(time.Now().UTC())
	tests := []struct {
		name   string
		mutate func(*IssueCredentialCommand)
	}{
		{`tenant`, func(value *IssueCredentialCommand) { value.TenantID = uuid.Nil }},
		{`registration`, func(value *IssueCredentialCommand) { value.RegistrationID = uuid.Nil }},
		{`series`, func(value *IssueCredentialCommand) { value.SeriesID = uuid.Nil }},
		{`instance`, func(value *IssueCredentialCommand) { value.InstanceID = uuid.Nil }},
		{`session`, func(value *IssueCredentialCommand) { value.SessionID = uuid.Nil }},
		{`principal`, func(value *IssueCredentialCommand) { value.PrincipalID = uuid.Nil }},
		{`epoch`, func(value *IssueCredentialCommand) { value.Epoch = 0 }},
		{`ttl zero`, func(value *IssueCredentialCommand) { value.TTL = 0 }},
		{`ttl too long`, func(value *IssueCredentialCommand) { value.TTL = MaxCredentialTTL + time.Nanosecond }},
		{`time`, func(value *IssueCredentialCommand) { value.At = time.Time{} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := base
			test.mutate(&command)
			if _, err := protector.Issue(command); !errors.Is(err, ErrInvalidCredential) {
				t.Fatalf(`Issue() error = %v, want %v`, err, ErrInvalidCredential)
			}
		})
	}
}

func TestCredentialProtectorReportsEntropyFailure(t *testing.T) {
	t.Parallel()

	protector := mustCredentialProtector(t, bytes.NewReader(make([]byte, 8)))
	_, err := protector.Issue(validIssueCredentialCommand(time.Now().UTC()))
	if !errors.Is(err, ErrCredentialEntropy) && !errors.Is(err, io.EOF) {
		t.Fatalf(`Issue() error = %v, want entropy failure`, err)
	}
}

func TestRevokeCredentialCreatesTerminalAuditableState(t *testing.T) {
	t.Parallel()

	protector := mustCredentialProtector(t, bytes.NewReader(sequentialEntropy(96)))
	issued, err := protector.Issue(validIssueCredentialCommand(time.Now().UTC()))
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	revokedAt := issued.Credential.IssuedAt.Add(time.Minute)
	revoked, err := RevokeCredential(
		issued.Credential,
		`  rotated after user request  `,
		revokedAt,
	)
	if err != nil {
		t.Fatalf(`RevokeCredential() error = %v`, err)
	}
	if revoked.CredentialStatus != CredentialStatusRevoked ||
		revoked.Version != 2 ||
		revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(revokedAt) ||
		revoked.RevocationReason == nil || *revoked.RevocationReason != `rotated after user request` ||
		!revoked.UpdatedAt.Equal(revokedAt) ||
		issued.Credential.CredentialStatus != CredentialStatusActive {
		t.Fatalf(`RevokeCredential() = %+v; source = %+v`, revoked, issued.Credential)
	}
	if err := ValidateCredential(revoked); err != nil {
		t.Fatalf(`ValidateCredential(revoked) error = %v`, err)
	}
	if _, err := RevokeCredential(revoked, `again`, revokedAt); !errors.Is(
		err,
		ErrCredentialTerminal,
	) {
		t.Fatalf(`second RevokeCredential() error = %v`, err)
	}
}

func TestCredentialExpiryUsesHalfOpenBoundary(t *testing.T) {
	t.Parallel()

	protector := mustCredentialProtector(t, bytes.NewReader(sequentialEntropy(96)))
	issued, err := protector.Issue(validIssueCredentialCommand(time.Now().UTC()))
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	if issued.Credential.Expired(issued.Credential.ExpiresAt.Add(-time.Nanosecond)) {
		t.Fatal(`credential expired before boundary`)
	}
	if !issued.Credential.Expired(issued.Credential.ExpiresAt) {
		t.Fatal(`credential remained active at expiry boundary`)
	}
}

func validIssueCredentialCommand(at time.Time) IssueCredentialCommand {
	return IssueCredentialCommand{
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		PrincipalID:    uuid.New(),
		Epoch:          1,
		TTL:            5 * time.Minute,
		At:             at,
	}
}

func mustCredentialProtector(
	t *testing.T,
	randomness io.Reader,
) *CredentialProtector {
	t.Helper()
	protector, err := NewCredentialProtector(bytes.Repeat([]byte{0xA5}, 32), randomness)
	if err != nil {
		t.Fatalf(`NewCredentialProtector() error = %v`, err)
	}
	return protector
}

func sequentialEntropy(size int) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = byte(index + 1)
	}
	return result
}
