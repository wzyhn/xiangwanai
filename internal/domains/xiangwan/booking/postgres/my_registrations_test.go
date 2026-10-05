package bookingpostgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestListUsesOwnedStableMixedDirectionKeyset(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 5, 0, 0, 0, time.UTC)
	first := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	second := myRegistrationFacts(asOf, asOf.Add(3*time.Hour))
	lookahead := myRegistrationFacts(asOf, asOf.Add(4*time.Hour))
	alignMyRegistrationOwner(&first, second.Registration)
	alignMyRegistrationOwner(&lookahead, second.Registration)

	call := 0
	var capturedQueries []string
	var capturedArgs [][]any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			call++
			capturedQueries = append(capturedQueries, query)
			capturedArgs = append(capturedArgs, append([]any(nil), args...))
			if call == 1 {
				return newFakeRows(
					mustMyRegistrationScanValues(t, first, asOf),
					mustMyRegistrationScanValues(t, second, asOf),
					mustMyRegistrationScanValues(t, lookahead, asOf),
				), nil
			}
			return newFakeRows(), nil
		},
	}}

	page, err := repository.List(context.Background(), booking.MyRegistrationFilter{
		TenantID:    second.Registration.TenantID,
		PrincipalID: second.Registration.PrincipalID,
		Limit:       2,
		At:          asOf,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.ActiveState != booking.MyRegistrationStateAll ||
		!page.AsOf.Equal(asOf) ||
		len(page.Items) != 2 ||
		page.Items[0].RegistrationID != first.Registration.ID ||
		page.Items[1].RegistrationID != second.Registration.ID ||
		page.NextCursor == "" {
		t.Fatalf("List() = %+v", page)
	}
	if !reflect.DeepEqual(capturedArgs[0], []any{
		second.Registration.TenantID,
		second.Registration.PrincipalID,
		asOf,
		booking.MyRegistrationStateAll,
		3,
	}) {
		t.Fatalf("first query args = %#v", capturedArgs[0])
	}
	for _, fragment := range []string{
		"registration_record.tenant_id = $1",
		"registration_record.principal_id = $2",
		"activity_instance.series_id = registration_record.series_id",
		"activity_session.instance_id = registration_record.instance_id",
		"order_record.principal_id = registration_record.principal_id",
		"refund_case.principal_id = registration_record.principal_id",
		"checkin_record.registration_id = registration_record.id",
		"checkin_record.principal_id = registration_record.principal_id",
		"($4 = 'all' OR view_state = $4)",
		"CASE WHEN sort_rank IN (1, 2) THEN sort_at END ASC",
		"CASE WHEN sort_rank NOT IN (1, 2) THEN sort_at END DESC",
		"LIMIT $5",
		"'paid_confirmed', 'settled_zero'",
	} {
		if !strings.Contains(capturedQueries[0], fragment) {
			t.Fatalf("first query does not contain %q", fragment)
		}
	}

	next, err := repository.List(context.Background(), booking.MyRegistrationFilter{
		TenantID:    second.Registration.TenantID,
		PrincipalID: second.Registration.PrincipalID,
		Limit:       2,
		Cursor:      page.NextCursor,
		At:          asOf.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("List(next) error = %v", err)
	}
	if len(next.Items) != 0 ||
		next.NextCursor != "" ||
		!next.AsOf.Equal(asOf) {
		t.Fatalf("List(next) = %+v", next)
	}
	if len(capturedArgs[1]) != 8 ||
		capturedArgs[1][0] != second.Registration.TenantID ||
		capturedArgs[1][1] != second.Registration.PrincipalID ||
		!capturedArgs[1][2].(time.Time).Equal(asOf) ||
		capturedArgs[1][3] != booking.MyRegistrationStateAll ||
		capturedArgs[1][4] != 2 ||
		!capturedArgs[1][5].(time.Time).Equal(
			*second.Session.SessionStartAt,
		) ||
		capturedArgs[1][6] != second.Registration.ID ||
		capturedArgs[1][7] != 3 {
		t.Fatalf("next query args = %#v", capturedArgs[1])
	}
	for _, fragment := range []string{
		"sort_rank > $5",
		"(sort_at, registration_id) > ($6, $7)",
		"(sort_at, registration_id) < ($6, $7)",
		"LIMIT $8",
	} {
		if !strings.Contains(capturedQueries[1], fragment) {
			t.Fatalf("next query does not contain %q", fragment)
		}
	}
}

func TestScanMyRegistrationItemKeepsIndependentAxes(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 5, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	addPendingRefund(&facts, asOf)

	item, err := scanMyRegistrationItem(
		&fakeRow{values: mustMyRegistrationScanValues(t, facts, asOf)},
		asOf,
	)
	if err != nil {
		t.Fatalf("scanMyRegistrationItem() error = %v", err)
	}
	if item.State != booking.MyRegistrationStateRefundProcessing ||
		item.ParticipationStatus != registration.ParticipationStatusCancelled ||
		item.Order == nil ||
		item.Order.PaymentStatus != payment.OrderStatusPaidConfirmed ||
		item.Refund == nil ||
		item.Refund.RefundStatus != refund.StatusPendingManual ||
		item.Checkin.Status != booking.CheckinStatusNotRecorded ||
		item.HasActiveAccess ||
		item.CanContinuePayment {
		t.Fatalf("item = %+v", item)
	}
}

func TestScanMyRegistrationItemHydratesDurableCheckin(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		checkin booking.CheckinSummary
	}{
		{
			name: "checked in",
			checkin: booking.CheckinSummary{
				Status:      booking.CheckinStatusCheckedIn,
				CheckedInAt: timePointer(asOf.Add(-30 * time.Minute)),
			},
		},
		{
			name: "revoked",
			checkin: booking.CheckinSummary{
				Status:      booking.CheckinStatusRevoked,
				CheckedInAt: timePointer(asOf.Add(-30 * time.Minute)),
				RevokedAt:   timePointer(asOf.Add(-10 * time.Minute)),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
			facts.Checkin = test.checkin
			item, err := scanMyRegistrationItem(
				&fakeRow{values: mustMyRegistrationScanValues(t, facts, asOf)},
				asOf,
			)
			if err != nil {
				t.Fatalf("scanMyRegistrationItem() error = %v", err)
			}
			if !reflect.DeepEqual(item.Checkin, test.checkin) {
				t.Fatalf("Checkin = %+v, want %+v", item.Checkin, test.checkin)
			}
		})
	}
}

func TestHydrateCheckinRejectsPartialOrDriftedFacts(t *testing.T) {
	t.Parallel()

	at := time.Now().UTC()
	tests := []struct {
		name      string
		status    sql.NullString
		checkedAt sql.NullTime
		revokedAt sql.NullTime
		updatedAt sql.NullTime
	}{
		{
			name:      "orphan timestamp",
			checkedAt: sql.NullTime{Time: at, Valid: true},
		},
		{
			name:   "missing checked timestamp",
			status: sql.NullString{String: string(booking.CheckinStatusCheckedIn), Valid: true},
		},
		{
			name:      "checked update drift",
			status:    sql.NullString{String: string(booking.CheckinStatusCheckedIn), Valid: true},
			checkedAt: sql.NullTime{Time: at, Valid: true},
			updatedAt: sql.NullTime{Time: at.Add(time.Second), Valid: true},
		},
		{
			name:      "revoked missing timestamp",
			status:    sql.NullString{String: string(booking.CheckinStatusRevoked), Valid: true},
			checkedAt: sql.NullTime{Time: at, Valid: true},
			updatedAt: sql.NullTime{Time: at, Valid: true},
		},
		{
			name:      "unknown status",
			status:    sql.NullString{String: "unknown", Valid: true},
			checkedAt: sql.NullTime{Time: at, Valid: true},
			updatedAt: sql.NullTime{Time: at, Valid: true},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := hydrateCheckin(
				test.status,
				test.checkedAt,
				test.revokedAt,
				test.updatedAt,
			)
			if !errors.Is(err, ErrMyRegistrationProjection) {
				t.Fatalf("hydrateCheckin() error = %v, want %v", err, ErrMyRegistrationProjection)
			}
		})
	}
}

func TestScanMyRegistrationItemRejectsSQLDomainDrift(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 5, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	values := mustMyRegistrationScanValues(t, facts, asOf)
	values[len(values)-4] = booking.MyRegistrationStateCancelled

	_, err := scanMyRegistrationItem(&fakeRow{values: values}, asOf)
	if !errors.Is(err, ErrMyRegistrationProjection) {
		t.Fatalf("scanMyRegistrationItem() error = %v", err)
	}
}

func TestListRejectsInvalidAndCrossContextCursors(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 5, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	item, err := booking.ProjectMyRegistration(facts, asOf)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	validFilter := booking.MyRegistrationFilter{
		TenantID:    facts.Registration.TenantID,
		PrincipalID: facts.Registration.PrincipalID,
		State:       booking.MyRegistrationStateRegistered,
		Limit:       1,
		At:          asOf,
	}
	cursor, err := encodeMyRegistrationsCursor(validFilter, item)
	if err != nil {
		t.Fatalf("encodeMyRegistrationsCursor() error = %v", err)
	}
	invalidFields := decodedMyRegistrationsCursor{
		Version:        1,
		TenantID:       validFilter.TenantID,
		PrincipalID:    validFilter.PrincipalID,
		FilterState:    validFilter.State,
		AsOf:           asOf,
		ItemState:      item.State,
		SortRank:       99,
		SortAt:         item.SortAt,
		RegistrationID: item.RegistrationID,
	}
	invalidJSON, err := json.Marshal(invalidFields)
	if err != nil {
		t.Fatalf("marshal invalid cursor: %v", err)
	}

	unexpectedQuery := func(string, ...any) (rowsScanner, error) {
		t.Fatal("invalid request unexpectedly reached PostgreSQL")
		return nil, nil
	}
	repository := &Repository{db: &fakeQueryExecutor{queryRows: unexpectedQuery}}
	tests := []struct {
		name    string
		filter  booking.MyRegistrationFilter
		wantErr error
	}{
		{
			name:    "tenant required",
			filter:  booking.MyRegistrationFilter{},
			wantErr: ErrInvalidMyRegistrationsFilter,
		},
		{
			name: "principal required",
			filter: booking.MyRegistrationFilter{
				TenantID: validFilter.TenantID,
			},
			wantErr: ErrInvalidMyRegistrationsFilter,
		},
		{
			name: "unknown state",
			filter: booking.MyRegistrationFilter{
				TenantID:    validFilter.TenantID,
				PrincipalID: validFilter.PrincipalID,
				State:       "unknown",
			},
			wantErr: ErrInvalidMyRegistrationsFilter,
		},
		{
			name: "oversized limit",
			filter: booking.MyRegistrationFilter{
				TenantID:    validFilter.TenantID,
				PrincipalID: validFilter.PrincipalID,
				Limit:       booking.MaxMyRegistrationsLimit + 1,
			},
			wantErr: ErrInvalidMyRegistrationsFilter,
		},
		{
			name: "malformed cursor",
			filter: withMyRegistrationCursor(
				validFilter,
				"not-base64!",
				asOf,
			),
			wantErr: ErrInvalidMyRegistrationsCursor,
		},
		{
			name: "unknown cursor field",
			filter: withMyRegistrationCursor(
				validFilter,
				base64.RawURLEncoding.EncodeToString(
					[]byte(`{"v":1,"unknown":true}`),
				),
				asOf,
			),
			wantErr: ErrInvalidMyRegistrationsCursor,
		},
		{
			name: "invalid cursor rank",
			filter: withMyRegistrationCursor(
				validFilter,
				base64.RawURLEncoding.EncodeToString(invalidJSON),
				asOf,
			),
			wantErr: ErrInvalidMyRegistrationsCursor,
		},
		{
			name: "tenant changed",
			filter: func() booking.MyRegistrationFilter {
				value := withMyRegistrationCursor(validFilter, cursor, asOf)
				value.TenantID = uuid.New()
				return value
			}(),
			wantErr: ErrStaleMyRegistrationsCursor,
		},
		{
			name: "principal changed",
			filter: func() booking.MyRegistrationFilter {
				value := withMyRegistrationCursor(validFilter, cursor, asOf)
				value.PrincipalID = uuid.New()
				return value
			}(),
			wantErr: ErrStaleMyRegistrationsCursor,
		},
		{
			name: "state changed",
			filter: func() booking.MyRegistrationFilter {
				value := withMyRegistrationCursor(validFilter, cursor, asOf)
				value.State = booking.MyRegistrationStateAll
				return value
			}(),
			wantErr: ErrStaleMyRegistrationsCursor,
		},
		{
			name: "cursor expired",
			filter: withMyRegistrationCursor(
				validFilter,
				cursor,
				asOf.Add(booking.MaxMyRegistrationsCursorAge+time.Second),
			),
			wantErr: ErrStaleMyRegistrationsCursor,
		},
		{
			name: "cursor too far in future",
			filter: withMyRegistrationCursor(
				validFilter,
				cursor,
				asOf.Add(-booking.MyRegistrationsFutureSkew-time.Second),
			),
			wantErr: ErrStaleMyRegistrationsCursor,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, gotErr := repository.List(context.Background(), test.filter)
			if !errors.Is(gotErr, test.wantErr) {
				t.Fatalf("List() error = %v, want %v", gotErr, test.wantErr)
			}
		})
	}
}

func TestListPreservesReadFailuresAndClosesRows(t *testing.T) {
	t.Parallel()

	readFailure := errors.New("read failed")
	filter := booking.MyRegistrationFilter{
		TenantID:    uuid.New(),
		PrincipalID: uuid.New(),
		At:          time.Now().UTC(),
	}
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return nil, readFailure
		},
	}}
	if _, err := repository.List(
		context.Background(),
		filter,
	); !errors.Is(err, readFailure) {
		t.Fatalf("List(query failure) error = %v", err)
	}

	rows := newFakeRows()
	rows.err = readFailure
	repository.db = &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return rows, nil
		},
	}
	if _, err := repository.List(
		context.Background(),
		filter,
	); !errors.Is(err, readFailure) {
		t.Fatalf("List(iteration failure) error = %v", err)
	}
	if !rows.closed {
		t.Fatal("List() did not close rows")
	}
}

func withMyRegistrationCursor(
	filter booking.MyRegistrationFilter,
	cursor string,
	at time.Time,
) booking.MyRegistrationFilter {
	filter.Cursor = cursor
	filter.At = at
	return filter
}

func myRegistrationFacts(
	now time.Time,
	start time.Time,
) booking.MyRegistrationFacts {
	tenantID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	principalID := uuid.New()
	end := start.Add(2 * time.Hour)
	deliveryMode := activity.DeliveryModeOffline
	area := activity.AreaCodeHeping
	venueName := "Tianjin AI Hub"
	address := "Innovation Road 1"
	confirmedAt := now.Add(-time.Hour)
	return booking.MyRegistrationFacts{
		Registration: registration.Registration{
			ID:                  uuid.New(),
			TenantID:            tenantID,
			SeriesID:            seriesID,
			InstanceID:          instanceID,
			SessionID:           sessionID,
			PrincipalID:         principalID,
			ParticipationStatus: registration.ParticipationStatusConfirmed,
			IdempotencyKey:      "registration:my-list",
			ConfirmedAt:         &confirmedAt,
			Version:             2,
			CreatedAt:           confirmedAt,
			UpdatedAt:           confirmedAt,
		},
		SeriesTitle: "Tianjin AI Gathering",
		Instance: activity.Instance{
			ID:        instanceID,
			TenantID:  tenantID,
			SeriesID:  seriesID,
			Title:     "September Gathering",
			Status:    activity.InstanceStatusPublished,
			Version:   3,
			CreatedAt: confirmedAt.Add(-time.Hour),
			UpdatedAt: confirmedAt,
		},
		Session: activity.Session{
			ID:             sessionID,
			TenantID:       tenantID,
			InstanceID:     instanceID,
			Title:          "AI Roundtable",
			Status:         activity.SessionStatusPublished,
			SessionStartAt: &start,
			SessionEndAt:   &end,
			DeliveryMode:   &deliveryMode,
			Area:           &area,
			VenueName:      &venueName,
			Address:        &address,
			Version:        3,
			CreatedAt:      confirmedAt.Add(-time.Hour),
			UpdatedAt:      confirmedAt,
		},
	}
}

func alignMyRegistrationOwner(
	facts *booking.MyRegistrationFacts,
	owner registration.Registration,
) {
	facts.Registration.TenantID = owner.TenantID
	facts.Registration.PrincipalID = owner.PrincipalID
	facts.Instance.TenantID = owner.TenantID
	facts.Session.TenantID = owner.TenantID
}

func addPendingRefund(facts *booking.MyRegistrationFacts, now time.Time) {
	cancelledAt := now.Add(time.Minute)
	reason := "user_cancelled"
	facts.Registration.ParticipationStatus =
		registration.ParticipationStatusCancelled
	facts.Registration.CancelledAt = &cancelledAt
	facts.Registration.CancellationReason = &reason
	facts.Registration.Version++
	facts.Registration.UpdatedAt = cancelledAt
	actualPaidCents := int64(9_000)
	paidAt := now.Add(-time.Minute)
	orderID := uuid.New()
	facts.Order = &payment.Order{
		ID:                 orderID,
		TenantID:           facts.Registration.TenantID,
		RegistrationID:     facts.Registration.ID,
		SeriesID:           facts.Registration.SeriesID,
		InstanceID:         facts.Registration.InstanceID,
		SessionID:          facts.Registration.SessionID,
		PrincipalID:        facts.Registration.PrincipalID,
		PaymentStatus:      payment.OrderStatusPaidConfirmed,
		OriginalPriceCents: 10_000,
		DiscountCents:      1_000,
		PayableCents:       9_000,
		ActualPaidCents:    &actualPaidCents,
		PaidAt:             &paidAt,
		Version:            2,
		CreatedAt:          now.Add(-2 * time.Minute),
		UpdatedAt:          paidAt,
	}
	convertedAt := paidAt
	facts.Hold = &payment.CapacityHold{
		ID:             uuid.New(),
		TenantID:       facts.Registration.TenantID,
		OrderID:        orderID,
		RegistrationID: facts.Registration.ID,
		SessionID:      facts.Registration.SessionID,
		HoldStatus:     payment.CapacityHoldStatusConverted,
		ExpiresAt:      now.Add(8 * time.Minute),
		ConvertedAt:    &convertedAt,
		Version:        2,
		CreatedAt:      now.Add(-2 * time.Minute),
		UpdatedAt:      paidAt,
	}
	facts.Refund = &refund.Case{
		ID:                   uuid.New(),
		TenantID:             facts.Registration.TenantID,
		OrderID:              orderID,
		RegistrationID:       facts.Registration.ID,
		SeriesID:             facts.Registration.SeriesID,
		InstanceID:           facts.Registration.InstanceID,
		SessionID:            facts.Registration.SessionID,
		PrincipalID:          facts.Registration.PrincipalID,
		RefundStatus:         refund.StatusPendingManual,
		ReasonCode:           refund.ReasonUserCancelled,
		RequestedRefundCents: actualPaidCents,
		Version:              1,
		CreatedAt:            now.Add(2 * time.Minute),
		UpdatedAt:            now.Add(2 * time.Minute),
	}
}

func mustMyRegistrationScanValues(
	t *testing.T,
	facts booking.MyRegistrationFacts,
	asOf time.Time,
) []any {
	t.Helper()
	item, err := booking.ProjectMyRegistration(facts, asOf)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	values := []any{
		facts.Registration.ID,
		facts.Registration.TenantID,
		facts.Registration.SeriesID,
		facts.Registration.InstanceID,
		facts.Registration.SessionID,
		facts.Registration.PrincipalID,
		facts.Registration.ParticipationStatus,
		facts.Registration.IdempotencyKey,
		nullTime(facts.Registration.ConfirmedAt),
		nullTime(facts.Registration.CancelledAt),
		nullString(facts.Registration.CancellationReason),
		facts.Registration.Version,
		facts.Registration.CreatedAt,
		facts.Registration.UpdatedAt,
		facts.SeriesTitle,
		facts.Instance.Title,
		facts.Instance.Status,
		facts.Instance.Version,
		facts.Instance.CreatedAt,
		facts.Instance.UpdatedAt,
		facts.Session.Title,
		facts.Session.Status,
		nullTime(facts.Session.SessionStartAt),
		nullTime(facts.Session.SessionEndAt),
		nullDeliveryMode(facts.Session.DeliveryMode),
		nullArea(facts.Session.Area),
		nullString(facts.Session.VenueName),
		nullString(facts.Session.Address),
		nullString(facts.Session.OnlineParticipationMode),
		facts.Session.Version,
		facts.Session.CreatedAt,
		facts.Session.UpdatedAt,
	}
	values = append(values, nullableOrderValues(facts.Order)...)
	values = append(values, nullableHoldValues(facts.Hold)...)
	values = append(values, nullableRefundValues(facts.Refund)...)
	values = append(values, nullableCheckinValues(facts.Checkin)...)
	return append(values,
		item.State,
		item.SortRank,
		item.LastBusinessAt,
		item.SortAt,
	)
}

func nullableCheckinValues(value booking.CheckinSummary) []any {
	switch value.Status {
	case ``, booking.CheckinStatusNotRecorded:
		return []any{
			sql.NullString{},
			sql.NullTime{},
			sql.NullTime{},
			sql.NullTime{},
		}
	case booking.CheckinStatusCheckedIn:
		return []any{
			sql.NullString{String: string(value.Status), Valid: true},
			nullTime(value.CheckedInAt),
			sql.NullTime{},
			nullTime(value.CheckedInAt),
		}
	case booking.CheckinStatusRevoked:
		return []any{
			sql.NullString{String: string(value.Status), Valid: true},
			nullTime(value.CheckedInAt),
			nullTime(value.RevokedAt),
			nullTime(value.RevokedAt),
		}
	default:
		return []any{
			sql.NullString{String: string(value.Status), Valid: true},
			nullTime(value.CheckedInAt),
			nullTime(value.RevokedAt),
			nullTime(value.RevokedAt),
		}
	}
}

func nullableOrderValues(value *payment.Order) []any {
	if value == nil {
		return []any{
			uuid.NullUUID{},
			sql.NullString{},
			sql.NullInt64{},
			sql.NullInt64{},
			sql.NullInt64{},
			sql.NullInt64{},
			sql.NullTime{},
			sql.NullTime{},
			sql.NullInt64{},
			sql.NullTime{},
			sql.NullTime{},
		}
	}
	return []any{
		uuid.NullUUID{UUID: value.ID, Valid: true},
		sql.NullString{String: string(value.PaymentStatus), Valid: true},
		sql.NullInt64{Int64: value.OriginalPriceCents, Valid: true},
		sql.NullInt64{Int64: value.DiscountCents, Valid: true},
		sql.NullInt64{Int64: value.PayableCents, Valid: true},
		nullInt64(value.ActualPaidCents),
		nullTime(value.PaidAt),
		nullTime(value.ClosedAt),
		sql.NullInt64{Int64: value.Version, Valid: true},
		sql.NullTime{Time: value.CreatedAt, Valid: true},
		sql.NullTime{Time: value.UpdatedAt, Valid: true},
	}
}

func nullableHoldValues(value *payment.CapacityHold) []any {
	if value == nil {
		return []any{
			uuid.NullUUID{},
			sql.NullString{},
			sql.NullTime{},
			sql.NullInt64{},
			sql.NullTime{},
			sql.NullTime{},
		}
	}
	return []any{
		uuid.NullUUID{UUID: value.ID, Valid: true},
		sql.NullString{String: string(value.HoldStatus), Valid: true},
		sql.NullTime{Time: value.ExpiresAt, Valid: true},
		sql.NullInt64{Int64: value.Version, Valid: true},
		sql.NullTime{Time: value.CreatedAt, Valid: true},
		sql.NullTime{Time: value.UpdatedAt, Valid: true},
	}
}

func nullableRefundValues(value *refund.Case) []any {
	if value == nil {
		return []any{
			uuid.NullUUID{},
			sql.NullString{},
			sql.NullString{},
			sql.NullInt64{},
			sql.NullInt64{},
			sql.NullTime{},
			sql.NullInt64{},
			sql.NullTime{},
			sql.NullTime{},
		}
	}
	return []any{
		uuid.NullUUID{UUID: value.ID, Valid: true},
		sql.NullString{String: string(value.RefundStatus), Valid: true},
		sql.NullString{String: string(value.ReasonCode), Valid: true},
		sql.NullInt64{Int64: value.RequestedRefundCents, Valid: true},
		sql.NullInt64{Int64: value.SuccessfulRefundCents, Valid: true},
		nullTime(value.ResolvedAt),
		sql.NullInt64{Int64: value.Version, Valid: true},
		sql.NullTime{Time: value.CreatedAt, Valid: true},
		sql.NullTime{Time: value.UpdatedAt, Valid: true},
	}
}

type fakeQueryExecutor struct {
	queryRows func(string, ...any) (rowsScanner, error)
}

func (executor *fakeQueryExecutor) queryContext(
	_ context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.queryRows(query, args...)
}

type fakeRow struct {
	values []any
	err    error
}

func (row *fakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("fake row destination count mismatch")
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		value := reflect.ValueOf(row.values[index])
		if target.Kind() != reflect.Pointer ||
			!value.IsValid() ||
			!value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fake row value type mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}

type fakeRows struct {
	values [][]any
	index  int
	err    error
	closed bool
}

func newFakeRows(values ...[]any) *fakeRows {
	return &fakeRows{values: values, index: -1}
}

func (rows *fakeRows) Next() bool {
	if rows.index+1 >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeRows) Scan(destinations ...any) error {
	if rows.index < 0 || rows.index >= len(rows.values) {
		return errors.New("fake rows Scan called without current row")
	}
	return (&fakeRow{values: rows.values[rows.index]}).Scan(destinations...)
}

func (rows *fakeRows) Err() error {
	return rows.err
}

func (rows *fakeRows) Close() error {
	rows.closed = true
	return nil
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func nullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullDeliveryMode(value *activity.DeliveryMode) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullArea(value *activity.AreaCode) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}
