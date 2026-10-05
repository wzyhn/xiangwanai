package bookingpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

func TestGetRegistrationDetailRequiresExactOwnerAndRegistration(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	var capturedQuery string
	var capturedArgs []any
	rows := newFakeRows(mustRegistrationDetailScanValues(
		t,
		facts,
		asOf,
		nil,
		nil,
	))
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return rows, nil
		},
	}}

	detail, err := repository.GetRegistrationDetail(
		context.Background(),
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		facts.Registration.ID,
		asOf,
	)
	if err != nil {
		t.Fatalf("GetRegistrationDetail() error = %v", err)
	}
	if detail.Item.RegistrationID != facts.Registration.ID ||
		detail.Item.SessionID != facts.Session.ID ||
		detail.Contact.Name != "王薇" ||
		detail.Contact.PhoneMasked != "+861****5678" ||
		!detail.CheckinCredentialEligible ||
		!detail.PrivateAccessEligible ||
		detail.AccessDenial != booking.MyRegistrationAccessAllowed ||
		!detail.AsOf.Equal(asOf) ||
		!rows.closed {
		t.Fatalf(
			"GetRegistrationDetail() = %+v, rows closed=%t",
			detail,
			rows.closed,
		)
	}
	for _, fragment := range []string{
		"registration_record.tenant_id = $1",
		"registration_record.principal_id = $2",
		"LEFT JOIN xiangwan_session_cancellation_receipts",
		"LEFT JOIN xiangwan_instance_cancellation_receipts",
		"LEFT JOIN xiangwan_registration_snapshots AS registration_snapshot",
		"registration_snapshot.tenant_id = registration_detail.tenant_id",
		"registration_snapshot.principal_id = registration_detail.principal_id",
		"registration_snapshot.registration_id = registration_detail.registration_id",
		"LEFT JOIN xiangwan_coupon_entries AS coupon_adjustment",
		"coupon_adjustment.tenant_id = registration_detail.tenant_id",
		"coupon_adjustment.principal_id = registration_detail.principal_id",
		"coupon_adjustment.order_id = registration_detail.order_id",
		"coupon_adjustment.registration_id = registration_detail.registration_id",
		"coupon_adjustment.refund_case_id = registration_detail.refund_case_id",
		"WHERE tenant_id = $1",
		"AND principal_id = $2",
		"AND registration_id = $4",
		"LIMIT 2",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("detail query does not contain %q", fragment)
		}
	}
	if !reflect.DeepEqual(capturedArgs, []any{
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		asOf,
		facts.Registration.ID,
	}) {
		t.Fatalf("detail args = %#v", capturedArgs)
	}
}

func TestScanMyRegistrationDetailRestoresPersistedCouponAdjustment(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	addPendingRefund(&facts, asOf.Add(-3*time.Minute))
	resolvedAt := asOf
	facts.Refund.RefundStatus = refund.StatusRefunded
	facts.Refund.SuccessfulRefundCents = facts.Refund.RequestedRefundCents
	facts.Refund.ResolvedAt = &resolvedAt
	facts.Refund.Version++
	facts.Refund.UpdatedAt = resolvedAt
	values := mustRegistrationDetailScanValues(t, facts, asOf, nil, nil)
	values[len(values)-3] = sql.NullString{
		String: string(coupon.EntryTypeRestored),
		Valid:  true,
	}
	values[len(values)-2] = sql.NullString{
		String: "coupon-refund-v2",
		Valid:  true,
	}
	values[len(values)-1] = sql.NullTime{Time: resolvedAt, Valid: true}

	detail, err := scanMyRegistrationDetail(&fakeRow{values: values}, asOf)
	if err != nil {
		t.Fatalf("scanMyRegistrationDetail() error = %v", err)
	}
	if detail.CouponAdjustment == nil ||
		detail.CouponAdjustment.Disposition != coupon.RefundDispositionRestore ||
		detail.CouponAdjustment.PolicyVersion != "coupon-refund-v2" ||
		!detail.CouponAdjustment.OccurredAt.Equal(resolvedAt) {
		t.Fatalf("coupon adjustment = %+v", detail.CouponAdjustment)
	}
}

func TestHydrateRegistrationCouponAdjustmentFailsClosedOnPartialOrUnknownFact(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		entryType     sql.NullString
		policyVersion sql.NullString
		occurredAt    sql.NullTime
	}{
		{
			name:       "entry without policy",
			entryType:  sql.NullString{String: "restored", Valid: true},
			occurredAt: sql.NullTime{Time: now, Valid: true},
		},
		{
			name:          "unknown terminal entry",
			entryType:     sql.NullString{String: "released", Valid: true},
			policyVersion: sql.NullString{String: "coupon-refund-v2", Valid: true},
			occurredAt:    sql.NullTime{Time: now, Valid: true},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := hydrateRegistrationCouponAdjustment(
				test.entryType,
				test.policyVersion,
				test.occurredAt,
			)
			if !errors.Is(err, ErrMyRegistrationProjection) {
				t.Fatalf("hydrateRegistrationCouponAdjustment() error = %v", err)
			}
		})
	}
}

func TestScanMyRegistrationDetailExplainsInstanceCancellation(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	cancelledAt := asOf.Add(time.Minute)
	cancelBookingFacts(&facts, cancelledAt)
	sessionReceipt := &testCancellationReceipt{
		id:     uuid.New(),
		reason: "Session unavailable",
		at:     cancelledAt,
	}
	instanceReceipt := &testCancellationReceipt{
		id:     uuid.New(),
		reason: "Whole Instance cancelled",
		at:     cancelledAt,
	}
	detail, err := scanMyRegistrationDetail(
		&fakeRow{values: mustRegistrationDetailScanValues(
			t,
			facts,
			asOf,
			sessionReceipt,
			instanceReceipt,
		)},
		asOf,
	)
	if err != nil {
		t.Fatalf("scanMyRegistrationDetail() error = %v", err)
	}
	if detail.Cancellation == nil ||
		detail.Cancellation.Scope !=
			booking.MyRegistrationCancellationScopeInstance ||
		detail.Cancellation.ReceiptID == nil ||
		*detail.Cancellation.ReceiptID != instanceReceipt.id ||
		detail.AccessDenial != booking.MyRegistrationAccessInstanceCancelled ||
		detail.CheckinCredentialEligible ||
		detail.PrivateAccessEligible {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestGetRegistrationDetailRejectsInvalidOrMissingIdentity(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	registrationID := uuid.New()
	unexpectedQuery := func(string, ...any) (rowsScanner, error) {
		t.Fatal("invalid identity unexpectedly reached PostgreSQL")
		return nil, nil
	}
	repository := &Repository{db: &fakeQueryExecutor{queryRows: unexpectedQuery}}
	for _, identity := range []struct {
		tenantID       uuid.UUID
		principalID    uuid.UUID
		registrationID uuid.UUID
	}{
		{principalID: principalID, registrationID: registrationID},
		{tenantID: tenantID, registrationID: registrationID},
		{tenantID: tenantID, principalID: principalID},
	} {
		if _, err := repository.GetRegistrationDetail(
			context.Background(),
			identity.tenantID,
			identity.principalID,
			identity.registrationID,
			time.Now(),
		); !errors.Is(err, ErrInvalidMyRegistrationIdentity) {
			t.Fatalf("GetRegistrationDetail(invalid) error = %v", err)
		}
	}

	repository.db = &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(), nil
		},
	}
	if _, err := repository.GetRegistrationDetail(
		context.Background(),
		tenantID,
		principalID,
		registrationID,
		time.Now(),
	); !errors.Is(err, ErrMyRegistrationNotFound) {
		t.Fatalf("GetRegistrationDetail(not found) error = %v", err)
	}
}

func TestGetRegistrationDetailFailsClosedOnMalformedReceiptOrDuplicate(
	t *testing.T,
) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	values := mustRegistrationDetailScanValues(t, facts, asOf, nil, nil)
	values[len(values)-10] = sql.NullString{
		String: "reason without receipt",
		Valid:  true,
	}
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(values), nil
		},
	}}
	if _, err := repository.GetRegistrationDetail(
		context.Background(),
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		facts.Registration.ID,
		asOf,
	); !errors.Is(err, ErrMyRegistrationProjection) {
		t.Fatalf("GetRegistrationDetail(malformed) error = %v", err)
	}

	valid := mustRegistrationDetailScanValues(t, facts, asOf, nil, nil)
	repository.db = &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(valid, valid), nil
		},
	}
	if _, err := repository.GetRegistrationDetail(
		context.Background(),
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		facts.Registration.ID,
		asOf,
	); !errors.Is(err, ErrMyRegistrationProjection) {
		t.Fatalf("GetRegistrationDetail(duplicate) error = %v", err)
	}
}

func TestGetRegistrationDetailFailsClosedOnMalformedContact(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	values := mustRegistrationDetailScanValues(t, facts, asOf, nil, nil)
	values[len(values)-5] = sql.NullString{String: " 王薇", Valid: true}
	values[len(values)-4] = sql.NullString{String: "13812345678", Valid: true}
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(values), nil
		},
	}}
	if _, err := repository.GetRegistrationDetail(
		context.Background(),
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		facts.Registration.ID,
		asOf,
	); !errors.Is(err, ErrMyRegistrationProjection) {
		t.Fatalf("GetRegistrationDetail(malformed contact) error = %v", err)
	}
}

func TestGetRegistrationDetailKeepsLegacyRegistrationWithoutSnapshot(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	values := mustRegistrationDetailScanValues(t, facts, asOf, nil, nil)
	values[len(values)-5] = sql.NullString{}
	values[len(values)-4] = sql.NullString{}
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(values), nil
		},
	}}

	detail, err := repository.GetRegistrationDetail(
		context.Background(),
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		facts.Registration.ID,
		asOf,
	)
	if err != nil {
		t.Fatalf("GetRegistrationDetail(legacy) error = %v", err)
	}
	if detail.Item.RegistrationID != facts.Registration.ID ||
		detail.Contact != (booking.MyRegistrationContact{}) {
		t.Fatalf("legacy detail = %+v", detail)
	}
}

type testCancellationReceipt struct {
	id     uuid.UUID
	reason string
	at     time.Time
}

func mustRegistrationDetailScanValues(
	t *testing.T,
	facts booking.MyRegistrationFacts,
	asOf time.Time,
	sessionReceipt *testCancellationReceipt,
	instanceReceipt *testCancellationReceipt,
) []any {
	t.Helper()
	values := mustMyRegistrationScanValues(t, facts, asOf)
	values = append(values, cancellationReceiptValues(sessionReceipt)...)
	values = append(values, cancellationReceiptValues(instanceReceipt)...)
	values = append(
		values,
		sql.NullString{String: "王薇", Valid: true},
		sql.NullString{String: "+8613812345678", Valid: true},
	)
	return append(
		values,
		sql.NullString{},
		sql.NullString{},
		sql.NullTime{},
	)
}

func cancellationReceiptValues(value *testCancellationReceipt) []any {
	if value == nil {
		return []any{uuid.NullUUID{}, sql.NullString{}, sql.NullTime{}}
	}
	return []any{
		uuid.NullUUID{UUID: value.id, Valid: true},
		sql.NullString{String: value.reason, Valid: true},
		sql.NullTime{Time: value.at, Valid: true},
	}
}

func cancelBookingFacts(
	facts *booking.MyRegistrationFacts,
	at time.Time,
) {
	reason := "instance_cancelled"
	facts.Registration.ParticipationStatus = "cancelled"
	facts.Registration.CancelledAt = &at
	facts.Registration.CancellationReason = &reason
	facts.Registration.Version++
	facts.Registration.UpdatedAt = at
	facts.Instance.Status = activity.InstanceStatusCancelled
	facts.Instance.UpdatedAt = at
	facts.Session.Status = activity.SessionStatusCancelled
	facts.Session.UpdatedAt = at
}
