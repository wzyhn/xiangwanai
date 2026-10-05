package checkinpostgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/google/uuid"
)

func TestCreateCredentialPersistsOnlyDigestsAndMetadata(t *testing.T) {
	t.Parallel()

	want := activeCredential(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: credentialScanValues(want)}
		},
	}}
	got, err := repository.CreateCredential(context.Background(), want)
	if err != nil {
		t.Fatalf(`CreateCredential() error = %v`, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(`CreateCredential() = %+v, want %+v`, got, want)
	}
	if !strings.Contains(capturedQuery, `INSERT INTO xiangwan_checkin_credentials`) ||
		len(capturedArgs) != 19 ||
		capturedArgs[0] != want.ID ||
		capturedArgs[7] != want.CredentialJTI ||
		!bytes.Equal(capturedArgs[8].([]byte), want.QRTokenHash[:]) ||
		!bytes.Equal(capturedArgs[9].([]byte), want.BackupCodeHash[:]) ||
		capturedArgs[10] != want.CredentialEpoch ||
		capturedArgs[11] != checkin.CredentialStatusActive {
		t.Fatalf(`CreateCredential query/args = %s %#v`, capturedQuery, capturedArgs)
	}
}

func TestCredentialLookupsAreTenantScopedAndLockRotationRows(t *testing.T) {
	t.Parallel()

	want := activeCredential(t)
	tests := []struct {
		name          string
		call          func(*Repository) (checkin.Credential, error)
		wantFragments []string
		wantArgs      func() []any
	}{
		{
			name: `identity`,
			call: func(repository *Repository) (checkin.Credential, error) {
				return repository.GetCredential(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{`tenant_id = $1 AND id = $2`},
			wantArgs:      func() []any { return []any{want.TenantID, want.ID} },
		},
		{
			name: `identity lock`,
			call: func(repository *Repository) (checkin.Credential, error) {
				return repository.GetCredentialForUpdate(
					context.Background(),
					want.TenantID,
					want.ID,
				)
			},
			wantFragments: []string{`tenant_id = $1 AND id = $2`, `FOR UPDATE`},
			wantArgs:      func() []any { return []any{want.TenantID, want.ID} },
		},
		{
			name: `public JTI`,
			call: func(repository *Repository) (checkin.Credential, error) {
				return repository.GetCredentialByJTI(
					context.Background(),
					want.TenantID,
					want.CredentialJTI,
				)
			},
			wantFragments: []string{`tenant_id = $1 AND credential_jti = $2`},
			wantArgs:      func() []any { return []any{want.TenantID, want.CredentialJTI} },
		},
		{
			name: `keyed backup digest`,
			call: func(repository *Repository) (checkin.Credential, error) {
				return repository.GetCredentialByBackupHash(
					context.Background(),
					want.TenantID,
					want.BackupCodeHash,
				)
			},
			wantFragments: []string{`tenant_id = $1 AND backup_code_hash = $2`},
			wantArgs: func() []any {
				return []any{want.TenantID, want.BackupCodeHash[:]}
			},
		},
		{
			name: `active Registration lock`,
			call: func(repository *Repository) (checkin.Credential, error) {
				return repository.GetActiveCredentialByRegistrationForUpdate(
					context.Background(),
					want.TenantID,
					want.RegistrationID,
				)
			},
			wantFragments: []string{
				`registration_id = $2`,
				`credential_status = 'active'`,
				`FOR UPDATE`,
			},
			wantArgs: func() []any { return []any{want.TenantID, want.RegistrationID} },
		},
		{
			name: `latest epoch lock`,
			call: func(repository *Repository) (checkin.Credential, error) {
				return repository.GetLatestCredentialByRegistrationForUpdate(
					context.Background(),
					want.TenantID,
					want.RegistrationID,
				)
			},
			wantFragments: []string{
				`registration_id = $2`,
				`ORDER BY credential_epoch DESC`,
				`LIMIT 1`,
				`FOR UPDATE`,
			},
			wantArgs: func() []any { return []any{want.TenantID, want.RegistrationID} },
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var capturedQuery string
			var capturedArgs []any
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(query string, args ...any) rowScanner {
					capturedQuery = query
					capturedArgs = append([]any(nil), args...)
					return &fakeRow{values: credentialScanValues(want)}
				},
			}}
			got, err := test.call(repository)
			if err != nil {
				t.Fatalf(`lookup error = %v`, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf(`lookup = %+v, want %+v`, got, want)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(capturedQuery, fragment) {
					t.Fatalf(`query %q missing %q`, capturedQuery, fragment)
				}
			}
			wantArgs := test.wantArgs()
			if len(capturedArgs) != len(wantArgs) {
				t.Fatalf(`args = %#v, want %#v`, capturedArgs, wantArgs)
			}
			for index := range wantArgs {
				gotBytes, gotIsBytes := capturedArgs[index].([]byte)
				wantBytes, wantIsBytes := wantArgs[index].([]byte)
				if gotIsBytes && wantIsBytes {
					if !bytes.Equal(gotBytes, wantBytes) {
						t.Fatalf(`arg %d = %x, want %x`, index, gotBytes, wantBytes)
					}
				} else if !reflect.DeepEqual(capturedArgs[index], wantArgs[index]) {
					t.Fatalf(`arg %d = %#v, want %#v`, index, capturedArgs[index], wantArgs[index])
				}
			}
		})
	}
}

func TestRevokeCredentialUsesOptimisticVersion(t *testing.T) {
	t.Parallel()

	current := activeCredential(t)
	want, err := checkin.RevokeCredential(
		current,
		`rotation`,
		current.IssuedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf(`RevokeCredential(domain) error = %v`, err)
	}
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: credentialScanValues(want)}
		},
	}}
	got, err := repository.RevokeCredential(context.Background(), want, current.Version)
	if err != nil {
		t.Fatalf(`RevokeCredential() error = %v`, err)
	}
	if !reflect.DeepEqual(got, want) ||
		!strings.Contains(capturedQuery, `version = version + 1`) ||
		!strings.Contains(capturedQuery, `version = $7`) ||
		len(capturedArgs) != 7 ||
		capturedArgs[6] != current.Version {
		t.Fatalf(`RevokeCredential result/query/args = %+v %s %#v`, got, capturedQuery, capturedArgs)
	}
}

func TestCredentialRepositoryMapsNoRowsAndRejectsBadDigestShape(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeRow{err: sql.ErrNoRows}
		},
	}}
	if _, err := repository.GetCredential(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf(`GetCredential() error = %v`, err)
	}
	if _, err := repository.RevokeCredential(
		context.Background(),
		activeCredential(t),
		1,
	); !errors.Is(err, ErrCredentialVersionConflict) {
		t.Fatalf(`RevokeCredential() error = %v`, err)
	}

	value := activeCredential(t)
	values := credentialScanValues(value)
	values[8] = []byte{1, 2, 3}
	if _, err := scanCredential(&fakeRow{values: values}); !errors.Is(
		err,
		checkin.ErrInvalidCredential,
	) {
		t.Fatalf(`scanCredential() error = %v`, err)
	}
}

func TestVerificationAttemptCreateAndReplayLookup(t *testing.T) {
	t.Parallel()

	want := knownVerificationAttempt(t)
	var queries []string
	var arguments [][]any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			return &fakeRow{values: verificationAttemptScanValues(want)}
		},
	}}
	created, err := repository.CreateVerificationAttempt(context.Background(), want)
	if err != nil {
		t.Fatalf(`CreateVerificationAttempt() error = %v`, err)
	}
	got, err := repository.GetVerificationAttemptByIdempotencyKey(
		context.Background(),
		want.TenantID,
		want.ActorID,
		want.IdempotencyKey,
	)
	if err != nil {
		t.Fatalf(`GetVerificationAttemptByIdempotencyKey() error = %v`, err)
	}
	byID, err := repository.GetVerificationAttempt(
		context.Background(),
		want.TenantID,
		want.ID,
	)
	if err != nil {
		t.Fatalf(`GetVerificationAttempt() error = %v`, err)
	}
	if !reflect.DeepEqual(created, want) ||
		!reflect.DeepEqual(got, want) ||
		!reflect.DeepEqual(byID, want) {
		t.Fatalf(`verification attempts = %+v %+v %+v, want %+v`, created, got, byID, want)
	}
	if len(queries) != 3 ||
		!strings.Contains(queries[0], `INSERT INTO xiangwan_checkin_verification_attempts`) ||
		len(arguments[0]) != 17 ||
		!bytes.Equal(
			arguments[0][9].([]byte),
			want.RequestFingerprint[:],
		) ||
		!strings.Contains(queries[1], `actor_id = $2`) ||
		!strings.Contains(queries[2], `tenant_id = $1 AND id = $2`) ||
		!reflect.DeepEqual(arguments[1], []any{
			want.TenantID,
			want.ActorID,
			want.IdempotencyKey,
		}) ||
		!reflect.DeepEqual(arguments[2], []any{want.TenantID, want.ID}) {
		t.Fatalf(`verification queries/args = %#v %#v`, queries, arguments)
	}
}

func TestVerificationAttemptLookupMapsNoRows(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeRow{err: sql.ErrNoRows}
		},
	}}
	_, err := repository.GetVerificationAttemptByIdempotencyKey(
		context.Background(),
		uuid.New(),
		uuid.New(),
		`verify:missing`,
	)
	if !errors.Is(err, ErrVerificationAttemptNotFound) {
		t.Fatalf(`lookup error = %v, want %v`, err, ErrVerificationAttemptNotFound)
	}
}

func activeCredential(t *testing.T) checkin.Credential {
	t.Helper()
	protector, err := checkin.NewCredentialProtector(
		bytes.Repeat([]byte{0xC3}, 32),
		bytes.NewReader(bytes.Repeat([]byte{0x5A}, 96)),
	)
	if err != nil {
		t.Fatalf(`NewCredentialProtector() error = %v`, err)
	}
	issued, err := protector.Issue(checkin.IssueCredentialCommand{
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		PrincipalID:    uuid.New(),
		Epoch:          3,
		TTL:            5 * time.Minute,
		At:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	return issued.Credential
}

func knownVerificationAttempt(t *testing.T) checkin.VerificationAttempt {
	t.Helper()
	credential := activeCredential(t)
	value, err := checkin.NewVerificationAttempt(checkin.NewVerificationAttemptCommand{
		TenantID:             credential.TenantID,
		RequestedSeriesID:    credential.SeriesID,
		RequestedInstanceID:  credential.InstanceID,
		RequestedSessionID:   credential.SessionID,
		ActorID:              uuid.New(),
		PresentedKind:        checkin.PresentedCredentialKindBackupCode,
		IdempotencyKey:       `verify:repo:001`,
		RequestFingerprint:   checkin.CredentialDigest{0xB1},
		CredentialMatched:    true,
		RegistrationEligible: true,
		Credential:           &credential,
		At:                   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf(`NewVerificationAttempt() error = %v`, err)
	}
	return value
}

func credentialScanValues(value checkin.Credential) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.CredentialJTI,
		append([]byte(nil), value.QRTokenHash[:]...),
		append([]byte(nil), value.BackupCodeHash[:]...),
		value.CredentialEpoch,
		value.CredentialStatus,
		value.IssuedAt,
		value.ExpiresAt,
		nullTime(value.RevokedAt),
		nullString(value.RevocationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func verificationAttemptScanValues(value checkin.VerificationAttempt) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.RequestedSeriesID,
		value.RequestedInstanceID,
		value.RequestedSessionID,
		value.ActorID,
		value.PresentedKind,
		value.Decision,
		value.IdempotencyKey,
		append([]byte(nil), value.RequestFingerprint[:]...),
		nullUUID(value.CredentialID),
		nullUUID(value.CredentialJTI),
		nullUUID(value.RegistrationID),
		nullUUID(value.PrincipalID),
		nullUUID(value.CheckinID),
		value.OccurredAt,
		value.CreatedAt,
	}
}
