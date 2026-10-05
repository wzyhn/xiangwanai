package couponpostgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

func testGrantor(
	tx *fakeGrantTransaction,
	policies GrantPolicyProvider,
	authorizer ManualGrantAuthorizer,
	now time.Time,
) *Grantor {
	return &Grantor{
		transactions: &fakeGrantTransactionStarter{tx: tx},
		pending:      &fakePendingInitialGuestLister{},
		policies:     policies,
		authorizer:   authorizer,
		now:          func() time.Time { return now },
	}
}

func knownInitialGrant(
	t *testing.T,
	source initialGuestSource,
	recordedAt time.Time,
) coupon.Grant {
	t.Helper()
	result, err := coupon.NewInitialGuestGrant(
		coupon.InitialGuestGrantCommand{
			TenantID:    source.Checkin.TenantID,
			PrincipalID: source.Checkin.PrincipalID,
			Source:      source.Facts,
			Policy:      knownGrantPolicy(),
			CheckedInAt: source.CheckedInEvent.OccurredAt,
			RecordedAt:  recordedAt,
		},
	)
	if err != nil {
		t.Fatalf("NewInitialGuestGrant() error = %v", err)
	}
	return result
}

func knownGrantPolicy() coupon.GrantPolicy {
	activityType := activity.ActivityTypeAIRoundtable
	return coupon.GrantPolicy{
		Configured:        true,
		PolicyVersion:     "coupon-policy-v1",
		FaceValueCents:    5000,
		Validity:          90 * 24 * time.Hour,
		ScopeType:         coupon.ScopeTypeActivityType,
		ScopeActivityType: &activityType,
		MinimumOrderCents: 5000,
	}
}

func knownManualCommand() ManualReplenishmentCommand {
	return ManualReplenishmentCommand{
		TenantID:       newID(),
		SourceCouponID: newID(),
		ActorID:        newID(),
		IdentityLinkID: newID(),
		BusinessKey:    "manual:support-001",
		Reason:         "coupon balance exhausted",
		Context:        "support-case-001",
	}
}

func newID() uuid.UUID {
	return uuid.New()
}

type fakeGrantTransactionStarter struct {
	tx      grantTransaction
	options *sql.TxOptions
}

func (starter *fakeGrantTransactionStarter) beginGrantTx(
	_ context.Context,
	options *sql.TxOptions,
) (grantTransaction, error) {
	starter.options = options
	return starter.tx, nil
}

type fakeGrantTransaction struct {
	source          initialGuestSource
	sourceErr       error
	lockedPrincipal uuid.UUID
	owner           uuid.UUID
	ownerErr        error
	ownerTenant     uuid.UUID
	ownerCoupon     uuid.UUID
	lockErr         error
	existing        coupon.Grant
	existingErr     error
	state           GrantState
	stateErr        error
	stateCalls      int
	created         *coupon.Grant
	createErr       error
	committed       bool
	commitErr       error
	rolledBack      bool
	audited         bool
	auditErr        error
}

func (*fakeGrantTransaction) authorizationQuery() CouponAuthorizationQuery {
	return fakeCouponAuthorizationQuery{}
}

func (tx *fakeGrantTransaction) loadCouponOwner(
	_ context.Context, tenantID uuid.UUID, couponID uuid.UUID,
) (uuid.UUID, error) {
	tx.ownerTenant = tenantID
	tx.ownerCoupon = couponID
	if tx.owner == uuid.Nil {
		tx.owner = newID()
	}
	return tx.owner, tx.ownerErr
}

func (tx *fakeGrantTransaction) auditManualGrant(
	context.Context, ManualReplenishmentCommand, uuid.UUID, coupon.Grant,
) error {
	tx.audited = true
	return tx.auditErr
}

func (tx *fakeGrantTransaction) lockInitialGuestSource(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (initialGuestSource, error) {
	return tx.source, tx.sourceErr
}

func (tx *fakeGrantTransaction) lockPrincipal(
	_ context.Context,
	principalID uuid.UUID,
) error {
	tx.lockedPrincipal = principalID
	return tx.lockErr
}

func (tx *fakeGrantTransaction) getGrantForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	coupon.GrantKind,
	string,
) (coupon.Grant, error) {
	return tx.existing, tx.existingErr
}

func (tx *fakeGrantTransaction) currentGrantState(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	time.Time,
) (GrantState, error) {
	tx.stateCalls++
	return tx.state, tx.stateErr
}

func (tx *fakeGrantTransaction) createGrant(
	_ context.Context,
	value coupon.Grant,
) (coupon.Grant, error) {
	tx.created = &value
	return value, tx.createErr
}

func (tx *fakeGrantTransaction) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeGrantTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

type fakeCouponAuthorizationQuery struct{}

func (fakeCouponAuthorizationQuery) QueryRowContext(
	context.Context,
	string,
	...any,
) *sql.Row {
	return nil
}

type fakeGrantPolicyProvider struct {
	policy coupon.GrantPolicy
	err    error
	calls  int
	at     time.Time
}

func (provider *fakeGrantPolicyProvider) CouponGrantPolicyAt(
	_ context.Context,
	_ CouponAuthorizationQuery,
	_ uuid.UUID,
	at time.Time,
) (coupon.GrantPolicy, error) {
	provider.calls++
	provider.at = at
	return provider.policy, provider.err
}

type fakeManualGrantAuthorizer struct {
	err            error
	calls          int
	tenantID       uuid.UUID
	actorID        uuid.UUID
	identityLinkID uuid.UUID
}

func (authorizer *fakeManualGrantAuthorizer) AuthorizeManualCouponGrant(
	_ context.Context, _ CouponAuthorizationQuery,
	tenantID, actorID, identityLinkID uuid.UUID,
) error {
	authorizer.calls++
	authorizer.tenantID = tenantID
	authorizer.actorID = actorID
	authorizer.identityLinkID = identityLinkID
	return authorizer.err
}

type fakePendingInitialGuestLister struct {
	sources []PendingInitialGuestSource
	err     error
	calls   int
	limit   int
}

func (lister *fakePendingInitialGuestLister) ListPendingInitialGuestSources(
	_ context.Context,
	_ uuid.UUID,
	limit int,
) ([]PendingInitialGuestSource, error) {
	lister.calls++
	lister.limit = limit
	return append([]PendingInitialGuestSource(nil), lister.sources...),
		lister.err
}
