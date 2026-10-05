package couponpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

func TestGrantInitialGuestCreatesFiveFromTrustedSource(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	source := knownInitialGuestSource(t, now.Add(-time.Minute), true)
	tx := &fakeGrantTransaction{
		source:      source,
		existingErr: ErrGrantNotFound,
	}
	policies := &fakeGrantPolicyProvider{policy: knownGrantPolicy()}
	grantor := testGrantor(tx, policies, &fakeManualGrantAuthorizer{}, now)

	result, err := grantor.GrantInitialGuest(
		context.Background(),
		InitialGuestGrantCommand{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		},
	)
	if err != nil {
		t.Fatalf("GrantInitialGuest() error = %v", err)
	}
	if result.Duplicate || len(result.Grant.Coupons) != coupon.GrantQuantity ||
		len(result.Grant.Entries) != coupon.GrantQuantity ||
		tx.created == nil || !tx.committed || tx.rolledBack ||
		tx.lockedPrincipal != source.Checkin.PrincipalID ||
		policies.calls != 1 || !policies.at.Equal(source.CheckedInEvent.OccurredAt) {
		t.Fatalf(
			"GrantInitialGuest() = %+v tx=%+v policies=%+v",
			result,
			tx,
			policies,
		)
	}
	for _, instrument := range result.Grant.Coupons {
		if instrument.SourceCheckin == nil ||
			*instrument.SourceCheckin != source.Checkin.ID ||
			instrument.SourceRoleBinding == nil ||
			*instrument.SourceRoleBinding != source.Facts.RoleBindingID ||
			!instrument.GrantedAt.Equal(source.CheckedInEvent.OccurredAt) {
			t.Fatalf("created Coupon = %+v", instrument)
		}
	}
}

func TestGrantInitialGuestReplaysWithoutCurrentPolicy(t *testing.T) {
	t.Parallel()

	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	source := knownInitialGuestSource(t, at, true)
	existing := knownInitialGrant(t, source, at.Add(time.Minute))
	tx := &fakeGrantTransaction{source: source, existing: existing}
	policies := &fakeGrantPolicyProvider{
		err: errors.New("configuration unavailable"),
	}
	grantor := testGrantor(tx, policies, &fakeManualGrantAuthorizer{}, at)

	result, err := grantor.GrantInitialGuest(
		context.Background(),
		InitialGuestGrantCommand{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		},
	)
	if err != nil || !result.Duplicate ||
		result.Grant.Coupons[0].ID != existing.Coupons[0].ID ||
		policies.calls != 0 || tx.created != nil || !tx.committed {
		t.Fatalf(
			"GrantInitialGuest(replay) = %+v, %v tx=%+v policies=%+v",
			result,
			err,
			tx,
			policies,
		)
	}
}

func TestGrantInitialGuestFailsClosedForEligibilityAndPolicy(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	tests := []struct {
		name     string
		source   initialGuestSource
		policy   coupon.GrantPolicy
		want     error
		wantCall int
	}{
		{
			name:     "not invited guest",
			source:   knownInitialGuestSource(t, now.Add(-time.Minute), false),
			policy:   knownGrantPolicy(),
			want:     ErrInitialGrantIneligible,
			wantCall: 0,
		},
		{
			name:     "policy incomplete",
			source:   knownInitialGuestSource(t, now.Add(-time.Minute), true),
			policy:   coupon.GrantPolicy{},
			want:     ErrGrantPolicyUnavailable,
			wantCall: 1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tx := &fakeGrantTransaction{
				source:      test.source,
				existingErr: ErrGrantNotFound,
			}
			policies := &fakeGrantPolicyProvider{policy: test.policy}
			grantor := testGrantor(
				tx,
				policies,
				&fakeManualGrantAuthorizer{},
				now,
			)
			_, err := grantor.GrantInitialGuest(
				context.Background(),
				InitialGuestGrantCommand{
					TenantID:  test.source.Checkin.TenantID,
					CheckinID: test.source.Checkin.ID,
				},
			)
			if !errors.Is(err, test.want) || tx.created != nil ||
				tx.committed || !tx.rolledBack ||
				policies.calls != test.wantCall {
				t.Fatalf(
					"GrantInitialGuest() error=%v tx=%+v policies=%+v",
					err,
					tx,
					policies,
				)
			}
		})
	}
}

func TestReplenishRequiresHistoryZeroBalanceAndAudit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	command := knownManualCommand()
	tx := &fakeGrantTransaction{
		existingErr: ErrGrantNotFound,
		state:       GrantState{HasHistory: true},
	}
	authorizer := &fakeManualGrantAuthorizer{}
	policies := &fakeGrantPolicyProvider{policy: knownGrantPolicy()}
	grantor := testGrantor(
		tx,
		policies,
		authorizer,
		now,
	)

	result, err := grantor.Replenish(context.Background(), command)
	if err != nil {
		t.Fatalf("Replenish() error = %v", err)
	}
	if result.Duplicate || len(result.Grant.Coupons) != coupon.GrantQuantity ||
		tx.created == nil || !tx.committed || tx.stateCalls != 1 ||
		!tx.audited || tx.lockedPrincipal != tx.owner ||
		tx.ownerTenant != command.TenantID || tx.ownerCoupon != command.SourceCouponID ||
		authorizer.calls != 1 || authorizer.tenantID != command.TenantID ||
		authorizer.actorID != command.ActorID ||
		authorizer.identityLinkID != command.IdentityLinkID || !policies.at.Equal(now) {
		t.Fatalf(
			"Replenish() = %+v tx=%+v authorizer=%+v",
			result,
			tx,
			authorizer,
		)
	}
	for _, instrument := range result.Grant.Coupons {
		if instrument.GrantedBy == nil ||
			*instrument.GrantedBy != command.ActorID ||
			instrument.GrantReason == nil ||
			*instrument.GrantReason != command.Reason ||
			instrument.GrantContext == nil ||
			*instrument.GrantContext != command.Context {
			t.Fatalf("manual Coupon = %+v", instrument)
		}
	}
}

func TestReplenishRollsBackWhenAuditCannotCommit(t *testing.T) {
	t.Parallel()
	command := knownManualCommand()
	auditErr := errors.New("audit unavailable")
	tx := &fakeGrantTransaction{
		existingErr: ErrGrantNotFound,
		state:       GrantState{HasHistory: true},
		auditErr:    auditErr,
	}
	grantor := testGrantor(tx, &fakeGrantPolicyProvider{policy: knownGrantPolicy()},
		&fakeManualGrantAuthorizer{}, time.Now().UTC())
	_, err := grantor.Replenish(context.Background(), command)
	if !errors.Is(err, auditErr) || !tx.audited || !tx.rolledBack || tx.committed {
		t.Fatalf("unaudited replenishment committed: err=%v tx=%+v", err, tx)
	}
}

func TestReplenishRejectsMissingSourceCoupon(t *testing.T) {
	t.Parallel()
	tx := &fakeGrantTransaction{ownerErr: ErrGrantSourceNotFound}
	grantor := testGrantor(tx, &fakeGrantPolicyProvider{policy: knownGrantPolicy()},
		&fakeManualGrantAuthorizer{}, time.Now().UTC())
	_, err := grantor.Replenish(context.Background(), knownManualCommand())
	if !errors.Is(err, ErrGrantSourceNotFound) || tx.lockedPrincipal != uuid.Nil ||
		tx.audited || tx.committed || !tx.rolledBack {
		t.Fatalf("missing source Coupon = %v tx=%+v", err, tx)
	}
}

func TestReplenishRejectsMissingHistoryOrPositiveBalance(t *testing.T) {
	t.Parallel()

	tests := []GrantState{
		{},
		{HasHistory: true, CurrentAvailable: 1},
	}
	for index, state := range tests {
		state := state
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			t.Parallel()
			tx := &fakeGrantTransaction{
				existingErr: ErrGrantNotFound,
				state:       state,
			}
			grantor := testGrantor(
				tx,
				&fakeGrantPolicyProvider{policy: knownGrantPolicy()},
				&fakeManualGrantAuthorizer{},
				time.Now().UTC(),
			)
			_, err := grantor.Replenish(
				context.Background(),
				knownManualCommand(),
			)
			if !errors.Is(err, ErrManualGrantForbidden) ||
				tx.created != nil || tx.committed || !tx.rolledBack {
				t.Fatalf(
					"Replenish(state=%+v) error=%v tx=%+v",
					state,
					err,
					tx,
				)
			}
		})
	}
}

func TestReplenishExactReplayPrecedesBalanceAndPolicy(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	command := knownManualCommand()
	principalID := newID()
	existing, err := coupon.NewManualReplenishment(
		coupon.ManualReplenishmentCommand{
			TenantID:    command.TenantID,
			PrincipalID: principalID,
			ActorID:     command.ActorID,
			BusinessKey: command.BusinessKey,
			Reason:      command.Reason,
			Context:     command.Context,
			Policy:      knownGrantPolicy(),
			GrantedAt:   now.Add(-time.Hour),
			RecordedAt:  now.Add(-time.Hour),
		},
	)
	if err != nil {
		t.Fatalf("NewManualReplenishment() error = %v", err)
	}
	tx := &fakeGrantTransaction{
		existing: existing,
		owner:    principalID,
		state: GrantState{
			HasHistory:       true,
			CurrentAvailable: coupon.GrantQuantity,
		},
	}
	policies := &fakeGrantPolicyProvider{
		err: errors.New("configuration unavailable"),
	}
	grantor := testGrantor(
		tx,
		policies,
		&fakeManualGrantAuthorizer{},
		now,
	)
	result, err := grantor.Replenish(context.Background(), command)
	if err != nil || !result.Duplicate || !tx.committed ||
		tx.stateCalls != 0 || policies.calls != 0 {
		t.Fatalf(
			"Replenish(replay) = %+v, %v tx=%+v policies=%+v",
			result,
			err,
			tx,
			policies,
		)
	}
}

func TestReconcilePendingUsesDurableSources(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	source := knownInitialGuestSource(t, now.Add(-time.Minute), true)
	tx := &fakeGrantTransaction{
		source:      source,
		existingErr: ErrGrantNotFound,
	}
	pending := &fakePendingInitialGuestLister{
		sources: []PendingInitialGuestSource{{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		}},
	}
	grantor := testGrantor(
		tx,
		&fakeGrantPolicyProvider{policy: knownGrantPolicy()},
		&fakeManualGrantAuthorizer{},
		now,
	)
	grantor.pending = pending
	results, err := grantor.ReconcilePending(
		context.Background(),
		source.Checkin.TenantID,
		25,
	)
	if err != nil || len(results) != 1 || results[0].Duplicate ||
		pending.calls != 1 || pending.limit != 25 {
		t.Fatalf(
			"ReconcilePending() = %+v, %v pending=%+v",
			results,
			err,
			pending,
		)
	}
}

func knownInitialGuestSource(
	t *testing.T,
	at time.Time,
	eligible bool,
) initialGuestSource {
	t.Helper()
	current, err := checkin.New(checkin.NewCommand{
		TenantID:       newID(),
		RegistrationID: newID(),
		SeriesID:       newID(),
		InstanceID:     newID(),
		SessionID:      newID(),
		PrincipalID:    newID(),
		CheckedInBy:    newID(),
		At:             at,
	})
	if err != nil {
		t.Fatalf("checkin.New() error = %v", err)
	}
	event, err := checkin.NewEvent(nil, current, "coupon:checked-in")
	if err != nil {
		t.Fatalf("checkin.NewEvent() error = %v", err)
	}
	result := initialGuestSource{
		Checkin:        current,
		CheckedInEvent: event,
		Eligible:       eligible,
	}
	if eligible {
		result.Facts = coupon.SourceFacts{
			PeopleProfileID: newID(),
			PeopleBindingID: newID(),
			RoleBindingID:   newID(),
			CheckinID:       current.ID,
			CheckinEventID:  event.ID,
		}
	}
	return result
}
