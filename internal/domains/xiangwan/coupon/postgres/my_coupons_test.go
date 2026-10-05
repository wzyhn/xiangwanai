package couponpostgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

func TestMyCouponsReaderUsesOwnedStableCouponKeyset(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	first := knownCouponLedger(t, asOf)
	second := knownCouponLedger(t, asOf)
	lookahead := knownCouponLedger(t, asOf)
	alignMyCouponOwner(&second, first)
	alignMyCouponOwner(&lookahead, first)
	first.Instrument.ExpiresAt = asOf.Add(time.Hour)
	second.Instrument.ExpiresAt = asOf.Add(2 * time.Hour)
	lookahead.Instrument.ExpiresAt = asOf.Add(3 * time.Hour)

	call := 0
	var capturedQueries []string
	var capturedArgs [][]any
	reader := &MyCouponsReader{db: &fakeMyCouponsQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			call++
			capturedQueries = append(capturedQueries, query)
			capturedArgs = append(
				capturedArgs,
				append([]any(nil), args...),
			)
			if call == 1 {
				return newFakeMyCouponRows(
					myCouponScanValues(
						first.Instrument,
						first.Entries[0],
						coupon.MyCouponStateAvailable,
					),
					myCouponScanValues(
						second.Instrument,
						second.Entries[0],
						coupon.MyCouponStateAvailable,
					),
					myCouponScanValues(
						lookahead.Instrument,
						lookahead.Entries[0],
						coupon.MyCouponStateAvailable,
					),
				), nil
			}
			return newFakeMyCouponRows(), nil
		},
	}}

	page, err := reader.List(context.Background(), coupon.MyCouponFilter{
		TenantID:    first.Instrument.TenantID,
		PrincipalID: first.Instrument.PrincipalID,
		Limit:       2,
		At:          asOf,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.ActiveState != coupon.MyCouponStateAll ||
		!page.AsOf.Equal(asOf) || len(page.Items) != 2 ||
		page.Items[0].CouponID != first.Instrument.ID ||
		page.Items[1].CouponID != second.Instrument.ID ||
		page.NextCursor == "" {
		t.Fatalf("My Coupons page = %+v", page)
	}
	if !reflect.DeepEqual(capturedArgs[0], []any{
		first.Instrument.TenantID,
		first.Instrument.PrincipalID,
		asOf,
		coupon.MyCouponStateAll,
		3,
	}) {
		t.Fatalf("first query args = %#v", capturedArgs[0])
	}
	for _, fragment := range []string{
		"instrument.tenant_id = $1",
		"instrument.principal_id = $2",
		"entry.principal_id = instrument.principal_id",
		"correction.entry_type = 'correction_required'",
		"correction.recorded_at <= $3",
		"instrument.created_at <= $3",
		"entry.recorded_at <= $3",
		"expires_at <= $3",
		"($4 = 'all' OR coupon_view_state = $4)",
		"coupon_sort_rank ASC, expires_at ASC, id ASC",
		"LIMIT $5",
	} {
		if !strings.Contains(capturedQueries[0], fragment) {
			t.Fatalf("first query does not contain %q", fragment)
		}
	}

	next, err := reader.List(context.Background(), coupon.MyCouponFilter{
		TenantID:    first.Instrument.TenantID,
		PrincipalID: first.Instrument.PrincipalID,
		Limit:       2,
		Cursor:      page.NextCursor,
		At:          asOf.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("List(next) error = %v", err)
	}
	if len(next.Items) != 0 || next.NextCursor != "" ||
		!next.AsOf.Equal(asOf) {
		t.Fatalf("My Coupons next page = %+v", next)
	}
	if len(capturedArgs[1]) != 8 ||
		capturedArgs[1][0] != first.Instrument.TenantID ||
		capturedArgs[1][1] != first.Instrument.PrincipalID ||
		!capturedArgs[1][2].(time.Time).Equal(asOf) ||
		capturedArgs[1][3] != coupon.MyCouponStateAll ||
		capturedArgs[1][4] != 1 ||
		!capturedArgs[1][5].(time.Time).Equal(
			second.Instrument.ExpiresAt,
		) ||
		capturedArgs[1][6] != second.Instrument.ID ||
		capturedArgs[1][7] != 3 {
		t.Fatalf("next query args = %#v", capturedArgs[1])
	}
	for _, fragment := range []string{
		"coupon_sort_rank > $5",
		"(expires_at, id) > ($6, $7)",
		"LIMIT $8",
	} {
		if !strings.Contains(capturedQueries[1], fragment) {
			t.Fatalf("next query does not contain %q", fragment)
		}
	}
}

func TestMyCouponsReaderGroupsCompleteLedgerAndChecksSQLProjection(
	t *testing.T,
) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	ledger := knownCouponLedger(t, asOf)
	order := knownOrderUseFacts(ledger, asOf)
	held := knownHoldEntry(t, ledger, order, asOf)
	ledger.Entries = append(ledger.Entries, held)
	values := [][]any{
		myCouponScanValues(
			ledger.Instrument,
			ledger.Entries[0],
			coupon.MyCouponStateHeld,
		),
		myCouponScanValues(
			ledger.Instrument,
			ledger.Entries[1],
			coupon.MyCouponStateHeld,
		),
	}
	reader := &MyCouponsReader{db: &fakeMyCouponsQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeMyCouponRows(values...), nil
		},
	}}
	page, err := reader.List(context.Background(), coupon.MyCouponFilter{
		TenantID:    ledger.Instrument.TenantID,
		PrincipalID: ledger.Instrument.PrincipalID,
		State:       coupon.MyCouponStateHeld,
		At:          asOf,
	})
	if err != nil {
		t.Fatalf("List(held) error = %v", err)
	}
	if len(page.Items) != 1 ||
		page.Items[0].State != coupon.MyCouponStateHeld ||
		page.Items[0].ActiveOrderID == nil ||
		*page.Items[0].ActiveOrderID != order.ID ||
		page.Items[0].LastEntrySequence != 2 {
		t.Fatalf("held page = %+v", page)
	}

	values[0][len(values[0])-2] = coupon.MyCouponStateRedeemed
	driftReader := &MyCouponsReader{db: &fakeMyCouponsQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeMyCouponRows(values...), nil
		},
	}}
	if _, err := driftReader.List(
		context.Background(),
		coupon.MyCouponFilter{
			TenantID:    ledger.Instrument.TenantID,
			PrincipalID: ledger.Instrument.PrincipalID,
			At:          asOf,
		},
	); !errors.Is(err, ErrMyCouponProjection) {
		t.Fatalf("List(projection drift) error = %v", err)
	}
}

func TestMyCouponsReaderRejectsInvalidAndCrossContextCursors(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	ledger := knownCouponLedger(t, asOf)
	item, err := coupon.ProjectMyCoupon(
		ledger.Instrument,
		ledger.Entries,
		asOf,
	)
	if err != nil {
		t.Fatalf("ProjectMyCoupon() error = %v", err)
	}
	validFilter := coupon.MyCouponFilter{
		TenantID:    ledger.Instrument.TenantID,
		PrincipalID: ledger.Instrument.PrincipalID,
		State:       coupon.MyCouponStateAvailable,
		Limit:       1,
		At:          asOf,
	}
	cursor, err := encodeMyCouponsCursor(validFilter, item)
	if err != nil {
		t.Fatalf("encodeMyCouponsCursor() error = %v", err)
	}
	unexpectedQuery := func(string, ...any) (rowsScanner, error) {
		t.Fatal("invalid request unexpectedly reached PostgreSQL")
		return nil, nil
	}
	reader := &MyCouponsReader{db: &fakeMyCouponsQueryExecutor{
		queryRows: unexpectedQuery,
	}}
	tests := []struct {
		name    string
		filter  coupon.MyCouponFilter
		wantErr error
	}{
		{
			name:    "ownership required",
			filter:  coupon.MyCouponFilter{},
			wantErr: ErrInvalidMyCouponsFilter,
		},
		{
			name: "unknown state",
			filter: coupon.MyCouponFilter{
				TenantID:    validFilter.TenantID,
				PrincipalID: validFilter.PrincipalID,
				State:       "unknown",
			},
			wantErr: ErrInvalidMyCouponsFilter,
		},
		{
			name: "limit too large",
			filter: coupon.MyCouponFilter{
				TenantID:    validFilter.TenantID,
				PrincipalID: validFilter.PrincipalID,
				Limit:       coupon.MaxMyCouponsLimit + 1,
			},
			wantErr: ErrInvalidMyCouponsFilter,
		},
		{
			name: "malformed cursor",
			filter: withMyCouponsCursor(
				validFilter,
				"not-base64!",
				asOf,
			),
			wantErr: ErrInvalidMyCouponsCursor,
		},
		{
			name: "unknown cursor field",
			filter: withMyCouponsCursor(
				validFilter,
				base64.RawURLEncoding.EncodeToString(
					[]byte(`{"v":1,"unknown":true}`),
				),
				asOf,
			),
			wantErr: ErrInvalidMyCouponsCursor,
		},
		{
			name: "tenant changed",
			filter: func() coupon.MyCouponFilter {
				value := withMyCouponsCursor(validFilter, cursor, asOf)
				value.TenantID = uuid.New()
				return value
			}(),
			wantErr: ErrStaleMyCouponsCursor,
		},
		{
			name: "principal changed",
			filter: func() coupon.MyCouponFilter {
				value := withMyCouponsCursor(validFilter, cursor, asOf)
				value.PrincipalID = uuid.New()
				return value
			}(),
			wantErr: ErrStaleMyCouponsCursor,
		},
		{
			name: "state changed",
			filter: func() coupon.MyCouponFilter {
				value := withMyCouponsCursor(validFilter, cursor, asOf)
				value.State = coupon.MyCouponStateAll
				return value
			}(),
			wantErr: ErrStaleMyCouponsCursor,
		},
		{
			name: "cursor expired",
			filter: withMyCouponsCursor(
				validFilter,
				cursor,
				asOf.Add(coupon.MaxMyCouponsCursorAge+time.Second),
			),
			wantErr: ErrStaleMyCouponsCursor,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, gotErr := reader.List(context.Background(), test.filter)
			if !errors.Is(gotErr, test.wantErr) {
				t.Fatalf("List() error = %v, want %v", gotErr, test.wantErr)
			}
		})
	}
}

func withMyCouponsCursor(
	filter coupon.MyCouponFilter,
	cursor string,
	at time.Time,
) coupon.MyCouponFilter {
	filter.Cursor = cursor
	filter.At = at
	return filter
}

func alignMyCouponOwner(target *Ledger, owner Ledger) {
	target.Instrument.TenantID = owner.Instrument.TenantID
	target.Instrument.PrincipalID = owner.Instrument.PrincipalID
	for index := range target.Entries {
		target.Entries[index].TenantID = owner.Instrument.TenantID
		target.Entries[index].PrincipalID = owner.Instrument.PrincipalID
	}
}

func myCouponScanValues(
	instrument coupon.Coupon,
	entry coupon.Entry,
	state coupon.MyCouponState,
) []any {
	return []any{
		instrument.ID,
		instrument.TenantID,
		instrument.PrincipalID,
		instrument.BenefitType,
		instrument.FaceValueCents,
		instrument.ScopeType,
		nullableActivityType(instrument.ScopeActivityType),
		nullableCouponUUID(instrument.ScopeSeriesID),
		instrument.MinimumOrderCents,
		instrument.ValidFrom,
		instrument.ExpiresAt,
		instrument.GrantKind,
		instrument.GrantBusinessKey,
		instrument.GrantOrdinal,
		instrument.PolicyVersion,
		nullableCouponUUID(instrument.SourcePeopleProfile),
		nullableCouponUUID(instrument.SourcePeopleBinding),
		nullableCouponUUID(instrument.SourceRoleBinding),
		nullableCouponUUID(instrument.SourceCheckin),
		nullableCouponUUID(instrument.SourceCheckinEvent),
		nullableCouponUUID(instrument.GrantedBy),
		nullableCouponString(instrument.GrantReason),
		nullableCouponString(instrument.GrantContext),
		instrument.GrantedAt,
		instrument.CreatedAt,
		entry.ID,
		entry.TenantID,
		entry.CouponID,
		entry.PrincipalID,
		entry.EntrySequence,
		entry.EntryType,
		entry.BusinessKey,
		nullableCouponUUID(entry.OrderID),
		nullableCouponUUID(entry.RegistrationID),
		nullableCouponUUID(entry.RelatedEntryID),
		nullableCouponUUID(entry.RefundCaseID),
		nullableCouponUUID(entry.SourceCheckinEventID),
		nullableCouponUUID(entry.ActorID),
		nullableCouponString(entry.Reason),
		nullableCouponString(entry.RefundPolicyVersion),
		entry.OccurredAt,
		entry.RecordedAt,
		state,
		coupon.MyCouponStateSortRank(state),
	}
}

func nullableActivityType(value *activity.ActivityType) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableCouponUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func nullableCouponString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

type fakeMyCouponsQueryExecutor struct {
	queryRows func(string, ...any) (rowsScanner, error)
}

func (executor *fakeMyCouponsQueryExecutor) queryContext(
	_ context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.queryRows(query, args...)
}

type fakeMyCouponRow struct {
	values []any
	err    error
}

func (row *fakeMyCouponRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("fake My Coupon row destination count mismatch")
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		value := reflect.ValueOf(row.values[index])
		if target.Kind() != reflect.Pointer || !value.IsValid() ||
			!value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fake My Coupon row value type mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}

type fakeMyCouponRows struct {
	values [][]any
	index  int
	err    error
	closed bool
}

func newFakeMyCouponRows(values ...[]any) *fakeMyCouponRows {
	return &fakeMyCouponRows{values: values, index: -1}
}

func (rows *fakeMyCouponRows) Next() bool {
	if rows.index+1 >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeMyCouponRows) Scan(destinations ...any) error {
	if rows.index < 0 || rows.index >= len(rows.values) {
		return errors.New("fake My Coupon rows Scan without current row")
	}
	return (&fakeMyCouponRow{values: rows.values[rows.index]}).
		Scan(destinations...)
}

func (rows *fakeMyCouponRows) Err() error {
	return rows.err
}

func (rows *fakeMyCouponRows) Close() error {
	rows.closed = true
	return nil
}
