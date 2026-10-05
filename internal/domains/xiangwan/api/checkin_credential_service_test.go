package xiangwanapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/google/uuid"
)

func TestCheckinCredentialServiceFixesOwnerScopeAndTTL(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	registrationID := uuid.New()
	ttl := 10 * time.Minute
	issued := knownIssuedCheckinCredential(
		t,
		tenantID,
		principalID,
		registrationID,
		ttl,
	)
	issuer := &fakeCheckinCredentialIssuer{issued: issued}
	service, err := NewCheckinCredentialService(tenantID, ttl, issuer)
	if err != nil {
		t.Fatalf("NewCheckinCredentialService() error = %v", err)
	}
	got, err := service.Issue(
		context.Background(),
		principalID,
		registrationID,
	)
	if err != nil || got.QRToken != issued.QRToken ||
		got.BackupCode != issued.BackupCode || issuer.calls != 1 ||
		issuer.command.TenantID != tenantID ||
		issuer.command.PrincipalID != principalID ||
		issuer.command.RegistrationID != registrationID ||
		issuer.command.TTL != ttl {
		t.Fatalf("Issue()=%+v, %v issuer=%+v", got, err, issuer)
	}
}

func TestCheckinCredentialServiceRejectsInvalidAndCrossOwnerResults(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	registrationID := uuid.New()
	issued := knownIssuedCheckinCredential(
		t,
		tenantID,
		principalID,
		registrationID,
		10*time.Minute,
	)
	issued.Credential.PrincipalID = uuid.New()
	issuer := &fakeCheckinCredentialIssuer{issued: issued}
	service, err := NewCheckinCredentialService(
		tenantID,
		10*time.Minute,
		issuer,
	)
	if err != nil {
		t.Fatalf("NewCheckinCredentialService() error = %v", err)
	}
	if _, err := service.Issue(
		context.Background(),
		principalID,
		registrationID,
	); !errors.Is(err, checkinpostgres.ErrRegistrationCredentialTransactionConflict) {
		t.Fatalf("Issue(cross owner) error = %v", err)
	}
	if _, err := service.Issue(
		context.Background(),
		uuid.Nil,
		registrationID,
	); !errors.Is(err, ErrInvalidCheckinCredentialRequest) {
		t.Fatalf("Issue(nil owner) error = %v", err)
	}
}

func knownIssuedCheckinCredential(
	t *testing.T,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	registrationID uuid.UUID,
	ttl time.Duration,
) checkin.IssuedCredential {
	t.Helper()
	protector, err := checkin.NewCredentialProtector(
		[]byte("synthetic-checkin-credential-key-for-unit-tests"),
		&deterministicCredentialEntropy{},
	)
	if err != nil {
		t.Fatalf("NewCredentialProtector() error = %v", err)
	}
	issued, err := protector.Issue(checkin.IssueCredentialCommand{
		TenantID:       tenantID,
		RegistrationID: registrationID,
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		PrincipalID:    principalID,
		Epoch:          1,
		TTL:            ttl,
		At:             time.Date(2026, time.September, 14, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Issue(domain) error = %v", err)
	}
	return issued
}

type deterministicCredentialEntropy struct {
	next byte
}

func (reader *deterministicCredentialEntropy) Read(target []byte) (int, error) {
	for index := range target {
		reader.next++
		target[index] = reader.next
	}
	return len(target), nil
}

type fakeCheckinCredentialIssuer struct {
	issued checkin.IssuedCredential
	err    error

	calls   int
	command checkinpostgres.IssueRegistrationCredentialCommand
}

func (fake *fakeCheckinCredentialIssuer) Issue(
	_ context.Context,
	command checkinpostgres.IssueRegistrationCredentialCommand,
) (checkin.IssuedCredential, error) {
	fake.calls++
	fake.command = command
	return fake.issued, fake.err
}
