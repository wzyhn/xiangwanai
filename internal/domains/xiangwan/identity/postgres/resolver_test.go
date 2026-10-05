package identitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	resolverTestAppID         = "wx1234567890abcdef"
	resolverTestPrivacyPolicy = "privacy-v1"
)

func TestResolverCreatesPrincipalLinkAndProductLoginAtomically(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	generationID := uuid.New()
	occurredAt := time.Date(2026, time.September, 27, 2, 3, 4, 567000000, time.UTC)
	providerIdentity := identity.ProviderIdentity{OpenID: "openid-1", UnionID: "unionid-1"}
	tx := &fakeIdentityTransaction{findExactErr: ErrIdentityLinkNotFound}
	starter := &fakeIdentityTransactionStarter{transactions: []*fakeIdentityTransaction{tx}}
	resolver, err := newResolver(
		tenantID,
		generationID,
		resolverTestAppID,
		resolverTestPrivacyPolicy,
		starter,
	)
	if err != nil {
		t.Fatalf("newResolver() error = %v", err)
	}
	result, err := resolver.Resolve(context.Background(), providerIdentity, occurredAt)
	if err != nil || result.ID == uuid.Nil || !result.IsNew {
		t.Fatalf("Resolve() = %+v, %v", result, err)
	}
	if starter.options[0].Isolation != sql.LevelSerializable ||
		tx.generationLockCalls != 1 || tx.generationTenantID != tenantID ||
		tx.generationID != generationID || tx.lockCalls != 1 ||
		tx.lockAppID != resolverTestAppID || tx.lockIdentity != providerIdentity ||
		tx.createdPrincipalID != result.ID || tx.createdTenantID != tenantID ||
		tx.linkPrincipalID != result.ID || tx.linkIdentity != providerIdentity ||
		tx.loginPrincipalID != result.ID || tx.loginAppID != resolverTestAppID ||
		tx.collectionPrincipalID != result.ID ||
		tx.collectionPrivacyPolicy != resolverTestPrivacyPolicy ||
		tx.commitCalls != 1 || tx.rollbackCalls != 0 {
		t.Fatalf("starter=%+v tx=%+v", starter, tx)
	}
}

func TestResolverReusesExactAndUnionPrincipals(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	existingID := uuid.New()
	exactLinkID := uuid.New()
	providerIdentity := identity.ProviderIdentity{OpenID: "openid-2", UnionID: "unionid-2"}
	exactTx := &fakeIdentityTransaction{
		exact: identityLinkState{
			ID: exactLinkID,
			Principal: principalState{
				ID:     existingID,
				Status: "active",
			},
		},
		unionOwners: []uuid.UUID{existingID},
	}
	exactResolver, err := newResolver(
		tenantID,
		uuid.New(),
		resolverTestAppID,
		resolverTestPrivacyPolicy,
		&fakeIdentityTransactionStarter{transactions: []*fakeIdentityTransaction{exactTx}},
	)
	if err != nil {
		t.Fatalf("newResolver() error = %v", err)
	}
	exactResult, err := exactResolver.Resolve(context.Background(), providerIdentity, time.Now())
	if err != nil || exactResult.ID != existingID || exactResult.IsNew ||
		exactTx.setUnionLinkID != exactLinkID ||
		exactTx.setUnionID != providerIdentity.UnionID || exactTx.createLinkCalls != 0 {
		t.Fatalf("Resolve(exact) = %+v, %v; tx=%+v", exactResult, err, exactTx)
	}

	unionTx := &fakeIdentityTransaction{
		findExactErr: ErrIdentityLinkNotFound,
		unionOwners:  []uuid.UUID{existingID, existingID},
		principal:    principalState{ID: existingID, Status: "active"},
	}
	unionResolver, err := newResolver(
		tenantID,
		uuid.New(),
		resolverTestAppID,
		resolverTestPrivacyPolicy,
		&fakeIdentityTransactionStarter{transactions: []*fakeIdentityTransaction{unionTx}},
	)
	if err != nil {
		t.Fatalf("newResolver(union) error = %v", err)
	}
	unionResult, err := unionResolver.Resolve(context.Background(), providerIdentity, time.Now())
	if err != nil || unionResult.ID != existingID || unionResult.IsNew ||
		unionTx.createdPrincipalID != uuid.Nil || unionTx.linkPrincipalID != existingID {
		t.Fatalf("Resolve(union) = %+v, %v; tx=%+v", unionResult, err, unionTx)
	}
}

func TestResolverRejectsCrossPrincipalUnionAndInactivePrincipal(t *testing.T) {
	t.Parallel()

	providerIdentity := identity.ProviderIdentity{OpenID: "openid-3", UnionID: "unionid-3"}
	for _, test := range []struct {
		name    string
		tx      *fakeIdentityTransaction
		wantErr error
	}{
		{
			name: "cross principal union",
			tx: &fakeIdentityTransaction{
				findExactErr: ErrIdentityLinkNotFound,
				unionOwners:  []uuid.UUID{uuid.New(), uuid.New()},
			},
			wantErr: ErrIdentityConflict,
		},
		{
			name: "inactive exact principal",
			tx: &fakeIdentityTransaction{exact: identityLinkState{
				ID: uuid.New(),
				Principal: principalState{
					ID:     uuid.New(),
					Status: "suspended",
				},
			}},
			wantErr: ErrPrincipalUnavailable,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolver, err := newResolver(
				uuid.New(),
				uuid.New(),
				resolverTestAppID,
				resolverTestPrivacyPolicy,
				&fakeIdentityTransactionStarter{transactions: []*fakeIdentityTransaction{test.tx}},
			)
			if err != nil {
				t.Fatalf("newResolver() error = %v", err)
			}
			if _, err := resolver.Resolve(
				context.Background(),
				providerIdentity,
				time.Now(),
			); !errors.Is(err, test.wantErr) || test.tx.commitCalls != 0 ||
				test.tx.rollbackCalls != 1 {
				t.Fatalf("Resolve() error=%v tx=%+v", err, test.tx)
			}
		})
	}
}

func TestResolverRetriesSerializableConflict(t *testing.T) {
	t.Parallel()

	providerIdentity := identity.ProviderIdentity{OpenID: "openid-4"}
	first := &fakeIdentityTransaction{
		findExactErr: ErrIdentityLinkNotFound,
		commitErr:    &pgconn.PgError{Code: "40001"},
	}
	second := &fakeIdentityTransaction{findExactErr: ErrIdentityLinkNotFound}
	starter := &fakeIdentityTransactionStarter{
		transactions: []*fakeIdentityTransaction{first, second},
	}
	resolver, err := newResolver(
		uuid.New(),
		uuid.New(),
		resolverTestAppID,
		resolverTestPrivacyPolicy,
		starter,
	)
	if err != nil {
		t.Fatalf("newResolver() error = %v", err)
	}
	result, err := resolver.Resolve(context.Background(), providerIdentity, time.Now())
	if err != nil || result.ID == uuid.Nil || !result.IsNew ||
		first.rollbackCalls != 1 || second.commitCalls != 1 || starter.calls != 2 {
		t.Fatalf("Resolve(retry) = %+v, %v; starter=%+v", result, err, starter)
	}
}

func TestResolverRejectsInactiveGenerationBeforeIdentityWrites(t *testing.T) {
	t.Parallel()

	tx := &fakeIdentityTransaction{generationErr: ErrIdentityGenerationInactive}
	resolver, err := newResolver(
		uuid.New(),
		uuid.New(),
		resolverTestAppID,
		resolverTestPrivacyPolicy,
		&fakeIdentityTransactionStarter{transactions: []*fakeIdentityTransaction{tx}},
	)
	if err != nil {
		t.Fatalf("newResolver() error = %v", err)
	}
	_, err = resolver.Resolve(
		context.Background(),
		identity.ProviderIdentity{OpenID: "openid-stale-generation"},
		time.Now(),
	)
	if !errors.Is(err, ErrIdentityGenerationInactive) ||
		tx.generationLockCalls != 1 || tx.lockCalls != 0 ||
		tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("Resolve(stale generation) error=%v tx=%+v", err, tx)
	}
}

func TestResolverStartsWithoutPrivacyPolicyButResolveFailsClosed(t *testing.T) {
	t.Parallel()

	starter := &fakeIdentityTransactionStarter{}
	resolver, err := newResolver(
		uuid.New(),
		uuid.New(),
		resolverTestAppID,
		"",
		starter,
	)
	if err != nil {
		t.Fatalf("newResolver(empty policy) error = %v", err)
	}
	_, err = resolver.Resolve(
		context.Background(),
		identity.ProviderIdentity{OpenID: "openid-policy-unavailable"},
		time.Now(),
	)
	if !errors.Is(err, ErrInvalidResolver) || starter.calls != 0 {
		t.Fatalf("Resolve(empty policy) error=%v starter calls=%d", err, starter.calls)
	}
}

type fakeIdentityTransactionStarter struct {
	transactions []*fakeIdentityTransaction
	options      []*sql.TxOptions
	calls        int
}

func (starter *fakeIdentityTransactionStarter) beginIdentityTx(
	_ context.Context,
	options *sql.TxOptions,
) (identityTransaction, error) {
	starter.options = append(starter.options, options)
	index := starter.calls
	starter.calls++
	if index >= len(starter.transactions) {
		return nil, errors.New("unexpected identity transaction")
	}
	return starter.transactions[index], nil
}

type fakeIdentityTransaction struct {
	exact         identityLinkState
	findExactErr  error
	unionOwners   []uuid.UUID
	unionErr      error
	principal     principalState
	principalErr  error
	createErr     error
	linkErr       error
	setUnionErr   error
	loginErr      error
	commitErr     error
	generationErr error

	lockCalls               int
	generationLockCalls     int
	generationTenantID      uuid.UUID
	generationID            uuid.UUID
	lockAppID               string
	lockIdentity            identity.ProviderIdentity
	createdPrincipalID      uuid.UUID
	createdTenantID         uuid.UUID
	linkPrincipalID         uuid.UUID
	linkIdentity            identity.ProviderIdentity
	createLinkCalls         int
	setUnionLinkID          uuid.UUID
	setUnionID              string
	loginPrincipalID        uuid.UUID
	loginAppID              string
	collectionPrincipalID   uuid.UUID
	collectionPrivacyPolicy string
	commitCalls             int
	rollbackCalls           int
}

func (tx *fakeIdentityTransaction) lockActiveGeneration(
	_ context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	tx.generationLockCalls++
	tx.generationTenantID = tenantID
	tx.generationID = generationID
	return tx.generationErr
}

func (tx *fakeIdentityTransaction) lockProviderIdentity(
	_ context.Context,
	appID string,
	providerIdentity identity.ProviderIdentity,
) error {
	tx.lockCalls++
	tx.lockAppID = appID
	tx.lockIdentity = providerIdentity
	return nil
}

func (tx *fakeIdentityTransaction) findExactLink(
	_ context.Context,
	_ string,
	_ string,
) (identityLinkState, error) {
	return tx.exact, tx.findExactErr
}

func (tx *fakeIdentityTransaction) findUnionPrincipalIDs(
	_ context.Context,
	_ string,
) ([]uuid.UUID, error) {
	return append([]uuid.UUID(nil), tx.unionOwners...), tx.unionErr
}

func (tx *fakeIdentityTransaction) lockPrincipal(
	_ context.Context,
	_ uuid.UUID,
) (principalState, error) {
	return tx.principal, tx.principalErr
}

func (tx *fakeIdentityTransaction) createPrincipal(
	_ context.Context,
	principalID uuid.UUID,
	tenantID uuid.UUID,
	_ time.Time,
) error {
	tx.createdPrincipalID = principalID
	tx.createdTenantID = tenantID
	return tx.createErr
}

func (tx *fakeIdentityTransaction) createIdentityLink(
	_ context.Context,
	principalID uuid.UUID,
	_ string,
	providerIdentity identity.ProviderIdentity,
	_ time.Time,
) error {
	tx.createLinkCalls++
	tx.linkPrincipalID = principalID
	tx.linkIdentity = providerIdentity
	return tx.linkErr
}

func (tx *fakeIdentityTransaction) setLinkUnionID(
	_ context.Context,
	linkID uuid.UUID,
	unionID string,
) error {
	tx.setUnionLinkID = linkID
	tx.setUnionID = unionID
	return tx.setUnionErr
}

func (tx *fakeIdentityTransaction) recordLogin(
	_ context.Context,
	principalID uuid.UUID,
	appID string,
	_ time.Time,
) error {
	tx.loginPrincipalID = principalID
	tx.loginAppID = appID
	return tx.loginErr
}

func (tx *fakeIdentityTransaction) recordCollectionBasis(
	_ context.Context,
	_ uuid.UUID,
	principalID uuid.UUID,
	_ uuid.UUID,
	_ string,
	privacyPolicyVersion string,
	_ time.Time,
) error {
	tx.collectionPrincipalID = principalID
	tx.collectionPrivacyPolicy = privacyPolicyVersion
	return nil
}

func (tx *fakeIdentityTransaction) Commit() error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *fakeIdentityTransaction) Rollback() error {
	tx.rollbackCalls++
	return nil
}
