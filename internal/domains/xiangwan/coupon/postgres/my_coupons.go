package couponpostgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyCouponsFilter = errors.New(
		"invalid xiangwan My Coupons filter",
	)
	ErrInvalidMyCouponsCursor = errors.New(
		"invalid xiangwan My Coupons cursor",
	)
	ErrStaleMyCouponsCursor = errors.New(
		"stale xiangwan My Coupons cursor",
	)
	ErrMyCouponProjection = errors.New(
		"xiangwan My Coupon projection mismatch",
	)
)

type MyCouponsReader struct {
	db myCouponsQueryExecutor
}

func NewMyCouponsReader(db DBTX) *MyCouponsReader {
	return &MyCouponsReader{db: sqlMyCouponsQueryExecutor{db: db}}
}

func (reader *MyCouponsReader) List(
	ctx context.Context,
	filter coupon.MyCouponFilter,
) (coupon.MyCouponsPage, error) {
	if reader == nil || reader.db == nil {
		return coupon.MyCouponsPage{}, ErrInvalidMyCouponsFilter
	}
	normalized, cursor, err := normalizeMyCouponsFilter(filter)
	if err != nil {
		return coupon.MyCouponsPage{}, err
	}

	var query strings.Builder
	query.WriteString(myCouponsQueryPrefix)
	args := []any{
		normalized.TenantID,
		normalized.PrincipalID,
		normalized.At,
		normalized.State,
	}
	if cursor != nil {
		query.WriteString(`
      AND (
            coupon_sort_rank > $5
            OR (
                coupon_sort_rank = $5
                AND (expires_at, id) > ($6, $7)
            )
      )
`)
		args = append(
			args,
			cursor.SortRank,
			cursor.ExpiresAt,
			cursor.CouponID,
		)
	}
	query.WriteString(myCouponsPageOrder)
	query.WriteString(fmt.Sprintf("LIMIT $%d\n", len(args)+1))
	args = append(args, normalized.Limit+1)
	query.WriteString(myCouponsQuerySuffix)

	rows, err := reader.db.queryContext(ctx, query.String(), args...)
	if err != nil {
		return coupon.MyCouponsPage{}, fmt.Errorf(
			"list xiangwan My Coupons: %w",
			err,
		)
	}
	defer func() {
		_ = rows.Close()
	}()
	items, err := scanMyCouponItems(rows, normalized.At)
	if err != nil {
		return coupon.MyCouponsPage{}, err
	}

	nextCursor := ""
	if len(items) > normalized.Limit {
		items = items[:normalized.Limit]
		nextCursor, err = encodeMyCouponsCursor(
			normalized,
			items[len(items)-1],
		)
		if err != nil {
			return coupon.MyCouponsPage{}, err
		}
	}
	return coupon.MyCouponsPage{
		Items:       items,
		ActiveState: normalized.State,
		AsOf:        normalized.At,
		NextCursor:  nextCursor,
	}, nil
}

type myCouponsQueryExecutor interface {
	queryContext(context.Context, string, ...any) (rowsScanner, error)
}

type sqlMyCouponsQueryExecutor struct {
	db DBTX
}

func (executor sqlMyCouponsQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	if executor.db == nil {
		return nil, ErrInvalidMyCouponsFilter
	}
	return executor.db.QueryContext(ctx, query, args...)
}

const myCouponsQueryPrefix = `
WITH owned AS (
    SELECT
        instrument.*,
        latest_state.entry_type AS effective_entry_type,
        EXISTS (
            SELECT 1
            FROM xiangwan_coupon_entries AS correction
            WHERE correction.tenant_id = instrument.tenant_id
              AND correction.principal_id = instrument.principal_id
              AND correction.coupon_id = instrument.id
              AND correction.entry_type = 'correction_required'
              AND correction.occurred_at <= $3
              AND correction.recorded_at <= $3
        ) AS correction_required
    FROM xiangwan_coupons AS instrument
    JOIN LATERAL (
        SELECT entry.entry_type
        FROM xiangwan_coupon_entries AS entry
        WHERE entry.tenant_id = instrument.tenant_id
          AND entry.principal_id = instrument.principal_id
          AND entry.coupon_id = instrument.id
          AND entry.entry_type <> 'correction_required'
          AND entry.occurred_at <= $3
          AND entry.recorded_at <= $3
        ORDER BY entry.entry_sequence DESC
        LIMIT 1
    ) AS latest_state ON TRUE
    WHERE instrument.tenant_id = $1
      AND instrument.principal_id = $2
      AND instrument.benefit_type = 'roundtable_coupon'
      AND instrument.created_at <= $3
), classified AS (
    SELECT
        owned.*,
        CASE
            WHEN correction_required THEN 'correction_required'
            WHEN effective_entry_type = 'held' THEN 'held'
            WHEN effective_entry_type IN ('redeemed', 'forfeited')
                THEN 'redeemed'
            WHEN effective_entry_type = 'invalidated' THEN 'invalidated'
            WHEN effective_entry_type IN ('granted', 'released', 'restored')
                 AND expires_at <= $3 THEN 'expired'
            WHEN effective_entry_type IN ('granted', 'released', 'restored')
                THEN 'available'
            ELSE 'invalid'
        END AS coupon_view_state
    FROM owned
), ranked AS (
    SELECT
        classified.*,
        CASE coupon_view_state
            WHEN 'available' THEN 1
            WHEN 'held' THEN 2
            WHEN 'correction_required' THEN 3
            WHEN 'expired' THEN 4
            WHEN 'redeemed' THEN 5
            WHEN 'invalidated' THEN 6
            ELSE 99
        END AS coupon_sort_rank
    FROM classified
), page_coupons AS (
    SELECT *
    FROM ranked
    WHERE ($4 = 'all' OR coupon_view_state = $4)
`

const myCouponsPageOrder = `
    ORDER BY coupon_sort_rank ASC, expires_at ASC, id ASC
`

const myCouponsQuerySuffix = `
)
SELECT
    instrument.id, instrument.tenant_id, instrument.principal_id,
    instrument.benefit_type, instrument.face_value_cents,
    instrument.scope_type, instrument.scope_activity_type,
    instrument.scope_series_id, instrument.minimum_order_cents,
    instrument.valid_from, instrument.expires_at, instrument.grant_kind,
    instrument.grant_business_key, instrument.grant_ordinal,
    instrument.policy_version, instrument.source_people_profile_id,
    instrument.source_people_binding_id, instrument.source_role_binding_id,
    instrument.source_checkin_id, instrument.source_checkin_event_id,
    instrument.granted_by, instrument.grant_reason,
    instrument.grant_context, instrument.granted_at,
    instrument.created_at,
    entry.id, entry.tenant_id, entry.coupon_id, entry.principal_id,
    entry.entry_sequence, entry.entry_type, entry.business_key,
    entry.order_id, entry.registration_id, entry.related_entry_id,
    entry.refund_case_id, entry.source_checkin_event_id, entry.actor_id,
    entry.reason, entry.refund_policy_version,
    entry.occurred_at, entry.recorded_at,
    instrument.coupon_view_state,
    instrument.coupon_sort_rank
FROM page_coupons AS instrument
JOIN xiangwan_coupon_entries AS entry
  ON entry.tenant_id = instrument.tenant_id
 AND entry.principal_id = instrument.principal_id
 AND entry.coupon_id = instrument.id
 AND entry.occurred_at <= $3
 AND entry.recorded_at <= $3
ORDER BY
    instrument.coupon_sort_rank ASC,
    instrument.expires_at ASC,
    instrument.id ASC,
    entry.entry_sequence ASC
`

type scannedMyCouponRow struct {
	instrument coupon.Coupon
	entry      coupon.Entry
	state      coupon.MyCouponState
	sortRank   int
}

func scanMyCouponItems(
	rows rowsScanner,
	asOf time.Time,
) ([]coupon.MyCouponItem, error) {
	items := make([]coupon.MyCouponItem, 0)
	seen := make(map[uuid.UUID]struct{})
	var current *scannedMyCouponRow
	history := make([]coupon.Entry, 0)
	appendCurrent := func() error {
		if current == nil {
			return nil
		}
		item, err := coupon.ProjectMyCoupon(
			current.instrument,
			history,
			asOf,
		)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMyCouponProjection, err)
		}
		if item.State != current.state ||
			item.SortRank != current.sortRank {
			return fmt.Errorf(
				"%w: SQL=(%s,%d) domain=(%s,%d)",
				ErrMyCouponProjection,
				current.state,
				current.sortRank,
				item.State,
				item.SortRank,
			)
		}
		items = append(items, item)
		return nil
	}

	for rows.Next() {
		row, err := scanMyCouponRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan xiangwan My Coupon: %w", err)
		}
		couponID := row.instrument.ID
		if current == nil || current.instrument.ID != couponID {
			if err := appendCurrent(); err != nil {
				return nil, err
			}
			if _, duplicate := seen[couponID]; duplicate {
				return nil, fmt.Errorf(
					"%w: non-contiguous Coupon rows",
					ErrMyCouponProjection,
				)
			}
			seen[couponID] = struct{}{}
			current = &row
			history = history[:0]
		} else if current.state != row.state ||
			current.sortRank != row.sortRank {
			return nil, fmt.Errorf(
				"%w: inconsistent Coupon classification",
				ErrMyCouponProjection,
			)
		}
		history = append(history, row.entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan My Coupons: %w", err)
	}
	if err := appendCurrent(); err != nil {
		return nil, err
	}
	return items, nil
}

func scanMyCouponRow(row rowScanner) (scannedMyCouponRow, error) {
	var instrument coupon.Coupon
	var entry coupon.Entry
	var scopeActivity sql.NullString
	var scopeSeries uuid.NullUUID
	var sourceProfile uuid.NullUUID
	var sourceBinding uuid.NullUUID
	var sourceRole uuid.NullUUID
	var sourceCheckin uuid.NullUUID
	var sourceEvent uuid.NullUUID
	var grantedBy uuid.NullUUID
	var grantReason sql.NullString
	var grantContext sql.NullString
	var orderID uuid.NullUUID
	var registrationID uuid.NullUUID
	var relatedEntryID uuid.NullUUID
	var refundCaseID uuid.NullUUID
	var entrySourceEvent uuid.NullUUID
	var actorID uuid.NullUUID
	var entryReason sql.NullString
	var refundPolicyVersion sql.NullString
	var state coupon.MyCouponState
	var sortRank int
	err := row.Scan(
		&instrument.ID, &instrument.TenantID, &instrument.PrincipalID,
		&instrument.BenefitType, &instrument.FaceValueCents,
		&instrument.ScopeType, &scopeActivity, &scopeSeries,
		&instrument.MinimumOrderCents, &instrument.ValidFrom,
		&instrument.ExpiresAt, &instrument.GrantKind,
		&instrument.GrantBusinessKey, &instrument.GrantOrdinal,
		&instrument.PolicyVersion, &sourceProfile, &sourceBinding,
		&sourceRole, &sourceCheckin, &sourceEvent, &grantedBy,
		&grantReason, &grantContext, &instrument.GrantedAt,
		&instrument.CreatedAt,
		&entry.ID, &entry.TenantID, &entry.CouponID, &entry.PrincipalID,
		&entry.EntrySequence, &entry.EntryType, &entry.BusinessKey,
		&orderID, &registrationID, &relatedEntryID, &refundCaseID,
		&entrySourceEvent, &actorID, &entryReason,
		&refundPolicyVersion, &entry.OccurredAt, &entry.RecordedAt,
		&state, &sortRank,
	)
	if err != nil {
		return scannedMyCouponRow{}, err
	}
	if scopeActivity.Valid {
		value := activity.ActivityType(scopeActivity.String)
		instrument.ScopeActivityType = &value
	}
	instrument.ScopeSeriesID = nullableUUID(scopeSeries)
	instrument.SourcePeopleProfile = nullableUUID(sourceProfile)
	instrument.SourcePeopleBinding = nullableUUID(sourceBinding)
	instrument.SourceRoleBinding = nullableUUID(sourceRole)
	instrument.SourceCheckin = nullableUUID(sourceCheckin)
	instrument.SourceCheckinEvent = nullableUUID(sourceEvent)
	instrument.GrantedBy = nullableUUID(grantedBy)
	instrument.GrantReason = nullableString(grantReason)
	instrument.GrantContext = nullableString(grantContext)
	entry.OrderID = nullableUUID(orderID)
	entry.RegistrationID = nullableUUID(registrationID)
	entry.RelatedEntryID = nullableUUID(relatedEntryID)
	entry.RefundCaseID = nullableUUID(refundCaseID)
	entry.SourceCheckinEventID = nullableUUID(entrySourceEvent)
	entry.ActorID = nullableUUID(actorID)
	entry.Reason = nullableString(entryReason)
	entry.RefundPolicyVersion = nullableString(refundPolicyVersion)
	if coupon.ValidateCoupon(instrument) != nil ||
		coupon.ValidateEntry(entry) != nil ||
		entry.TenantID != instrument.TenantID ||
		entry.PrincipalID != instrument.PrincipalID ||
		entry.CouponID != instrument.ID ||
		!coupon.ValidMyCouponState(state) ||
		state == coupon.MyCouponStateAll ||
		sortRank != coupon.MyCouponStateSortRank(state) {
		return scannedMyCouponRow{}, ErrMyCouponProjection
	}
	return scannedMyCouponRow{
		instrument: instrument,
		entry:      entry,
		state:      state,
		sortRank:   sortRank,
	}, nil
}

type decodedMyCouponsCursor struct {
	Version     int                  `json:"v"`
	TenantID    uuid.UUID            `json:"tenant_id"`
	PrincipalID uuid.UUID            `json:"principal_id"`
	FilterState coupon.MyCouponState `json:"filter_state"`
	AsOf        time.Time            `json:"as_of"`
	ItemState   coupon.MyCouponState `json:"item_state"`
	SortRank    int                  `json:"sort_rank"`
	ExpiresAt   time.Time            `json:"expires_at"`
	CouponID    uuid.UUID            `json:"coupon_id"`
}

func normalizeMyCouponsFilter(
	filter coupon.MyCouponFilter,
) (coupon.MyCouponFilter, *decodedMyCouponsCursor, error) {
	if filter.TenantID == uuid.Nil || filter.PrincipalID == uuid.Nil {
		return coupon.MyCouponFilter{}, nil, ErrInvalidMyCouponsFilter
	}
	if filter.State == "" {
		filter.State = coupon.MyCouponStateAll
	}
	if !coupon.ValidMyCouponState(filter.State) {
		return coupon.MyCouponFilter{}, nil, ErrInvalidMyCouponsFilter
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = coupon.DefaultMyCouponsLimit
	case filter.Limit < 1 || filter.Limit > coupon.MaxMyCouponsLimit:
		return coupon.MyCouponFilter{}, nil, ErrInvalidMyCouponsFilter
	}
	requestedAt := filter.At
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	filter.At = requestedAt.UTC()
	if filter.Cursor == "" {
		return filter, nil, nil
	}
	cursor, err := decodeMyCouponsCursor(filter.Cursor)
	if err != nil {
		return coupon.MyCouponFilter{}, nil, err
	}
	if cursor.TenantID != filter.TenantID ||
		cursor.PrincipalID != filter.PrincipalID ||
		cursor.FilterState != filter.State {
		return coupon.MyCouponFilter{}, nil, ErrStaleMyCouponsCursor
	}
	if filter.At.Sub(cursor.AsOf) > coupon.MaxMyCouponsCursorAge ||
		cursor.AsOf.After(filter.At.Add(coupon.MyCouponsFutureSkew)) {
		return coupon.MyCouponFilter{}, nil, ErrStaleMyCouponsCursor
	}
	filter.At = cursor.AsOf.UTC()
	return filter, &cursor, nil
}

func encodeMyCouponsCursor(
	filter coupon.MyCouponFilter,
	item coupon.MyCouponItem,
) (string, error) {
	encoded, err := json.Marshal(decodedMyCouponsCursor{
		Version:     1,
		TenantID:    filter.TenantID,
		PrincipalID: filter.PrincipalID,
		FilterState: filter.State,
		AsOf:        filter.At.UTC(),
		ItemState:   item.State,
		SortRank:    item.SortRank,
		ExpiresAt:   item.ExpiresAt.UTC(),
		CouponID:    item.CouponID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode", ErrInvalidMyCouponsCursor)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMyCouponsCursor(value string) (decodedMyCouponsCursor, error) {
	if len(value) > 2048 {
		return decodedMyCouponsCursor{}, fmt.Errorf(
			"%w: payload is too large",
			ErrInvalidMyCouponsCursor,
		)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return decodedMyCouponsCursor{}, fmt.Errorf(
			"%w: malformed base64",
			ErrInvalidMyCouponsCursor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor decodedMyCouponsCursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedMyCouponsCursor{}, fmt.Errorf(
			"%w: malformed payload",
			ErrInvalidMyCouponsCursor,
		)
	}
	if err := ensureMyCouponsCursorEOF(decoder); err != nil {
		return decodedMyCouponsCursor{}, err
	}
	if cursor.Version != 1 ||
		cursor.TenantID == uuid.Nil ||
		cursor.PrincipalID == uuid.Nil ||
		!coupon.ValidMyCouponState(cursor.FilterState) ||
		!coupon.ValidMyCouponState(cursor.ItemState) ||
		cursor.ItemState == coupon.MyCouponStateAll ||
		cursor.AsOf.IsZero() || cursor.ExpiresAt.IsZero() ||
		cursor.SortRank != coupon.MyCouponStateSortRank(cursor.ItemState) ||
		cursor.CouponID == uuid.Nil ||
		(cursor.FilterState != coupon.MyCouponStateAll &&
			cursor.FilterState != cursor.ItemState) {
		return decodedMyCouponsCursor{}, fmt.Errorf(
			"%w: invalid fields",
			ErrInvalidMyCouponsCursor,
		)
	}
	cursor.AsOf = cursor.AsOf.UTC()
	cursor.ExpiresAt = cursor.ExpiresAt.UTC()
	return cursor, nil
}

func ensureMyCouponsCursorEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"%w: trailing payload",
			ErrInvalidMyCouponsCursor,
		)
	}
	return nil
}
