package bookingpostgres

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyOrdersFilter = booking.ErrInvalidMyOrdersFilter
	ErrInvalidMyOrdersCursor = booking.ErrInvalidMyOrdersCursor
	ErrStaleMyOrdersCursor   = booking.ErrStaleMyOrdersCursor
	ErrMyOrderProjection     = errors.New(
		"xiangwan My Order projection mismatch",
	)
)

const myOrderProjection = myRegistrationProjection + `,
    order_view_state,
    order_sort_rank,
    order_sort_at
`

const myOrdersQueryFilter = `
FROM ranked
WHERE order_id IS NOT NULL
  AND ($4 = 'all' OR order_view_state = $4)
`

const myOrdersOrder = `
ORDER BY
    order_sort_rank ASC,
    order_sort_at DESC,
    order_id DESC
`

func (repository *Repository) ListOrders(
	ctx context.Context,
	filter booking.MyOrderFilter,
) (booking.MyOrdersPage, error) {
	normalized, cursor, err := normalizeMyOrdersFilter(filter)
	if err != nil {
		return booking.MyOrdersPage{}, err
	}

	var query strings.Builder
	query.WriteString(myRegistrationsQueryPrefix)
	query.WriteString(myOrderProjection)
	query.WriteString(myOrdersQueryFilter)
	args := []any{
		normalized.TenantID,
		normalized.PrincipalID,
		normalized.At,
		normalized.State,
	}
	if cursor != nil {
		query.WriteString(`
  AND (
        order_sort_rank > $5
        OR (
            order_sort_rank = $5
            AND (order_sort_at, order_id) < ($6, $7)
        )
  )
`)
		args = append(
			args,
			cursor.SortRank,
			cursor.SortAt,
			cursor.OrderID,
		)
	}
	query.WriteString(myOrdersOrder)
	query.WriteString(fmt.Sprintf("LIMIT $%d\n", len(args)+1))
	args = append(args, normalized.Limit+1)

	rows, err := repository.db.queryContext(ctx, query.String(), args...)
	if err != nil {
		return booking.MyOrdersPage{}, fmt.Errorf(
			"list xiangwan My Orders: %w",
			err,
		)
	}
	defer func() {
		_ = rows.Close()
	}()

	items := make([]booking.MyOrderItem, 0, normalized.Limit+1)
	for rows.Next() {
		item, scanErr := scanMyOrderItem(rows, normalized.At)
		if scanErr != nil {
			return booking.MyOrdersPage{}, fmt.Errorf(
				"scan xiangwan My Order: %w",
				scanErr,
			)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return booking.MyOrdersPage{}, fmt.Errorf(
			"iterate xiangwan My Orders: %w",
			err,
		)
	}

	nextCursor := ""
	if len(items) > normalized.Limit {
		items = items[:normalized.Limit]
		nextCursor, err = encodeMyOrdersCursor(
			normalized,
			items[len(items)-1],
		)
		if err != nil {
			return booking.MyOrdersPage{}, err
		}
	}
	return booking.MyOrdersPage{
		Items:       items,
		ActiveState: normalized.State,
		AsOf:        normalized.At,
		NextCursor:  nextCursor,
	}, nil
}

type myOrderRowScanner struct {
	row      rowScanner
	state    booking.MyOrderState
	sortRank int
	sortAt   time.Time
}

func (scanner *myOrderRowScanner) Scan(destinations ...any) error {
	allDestinations := make([]any, 0, len(destinations)+3)
	allDestinations = append(allDestinations, destinations...)
	allDestinations = append(
		allDestinations,
		&scanner.state,
		&scanner.sortRank,
		&scanner.sortAt,
	)
	return scanner.row.Scan(allDestinations...)
}

func scanMyOrderItem(
	row rowScanner,
	asOf time.Time,
) (booking.MyOrderItem, error) {
	scanner := &myOrderRowScanner{row: row}
	reservation, err := scanMyRegistrationItem(scanner, asOf)
	if err != nil {
		return booking.MyOrderItem{}, err
	}
	item, err := booking.ProjectMyOrder(reservation)
	if err != nil {
		return booking.MyOrderItem{}, fmt.Errorf(
			"%w: %v",
			ErrMyOrderProjection,
			err,
		)
	}
	if item.State != scanner.state ||
		item.SortRank != scanner.sortRank ||
		!item.SortAt.Equal(scanner.sortAt) {
		return booking.MyOrderItem{}, fmt.Errorf(
			"%w: SQL=(%s,%d,%s) domain=(%s,%d,%s)",
			ErrMyOrderProjection,
			scanner.state,
			scanner.sortRank,
			scanner.sortAt.UTC().Format(time.RFC3339Nano),
			item.State,
			item.SortRank,
			item.SortAt.UTC().Format(time.RFC3339Nano),
		)
	}
	return item, nil
}

type decodedMyOrdersCursor struct {
	Version     int                  `json:"v"`
	TenantID    uuid.UUID            `json:"tenant_id"`
	PrincipalID uuid.UUID            `json:"principal_id"`
	FilterState booking.MyOrderState `json:"filter_state"`
	AsOf        time.Time            `json:"as_of"`
	ItemState   booking.MyOrderState `json:"item_state"`
	SortRank    int                  `json:"sort_rank"`
	SortAt      time.Time            `json:"sort_at"`
	OrderID     uuid.UUID            `json:"order_id"`
}

func normalizeMyOrdersFilter(
	filter booking.MyOrderFilter,
) (booking.MyOrderFilter, *decodedMyOrdersCursor, error) {
	if filter.TenantID == uuid.Nil || filter.PrincipalID == uuid.Nil {
		return booking.MyOrderFilter{}, nil, ErrInvalidMyOrdersFilter
	}
	if filter.State == "" {
		filter.State = booking.MyOrderStateAll
	}
	if !booking.ValidMyOrderState(filter.State) {
		return booking.MyOrderFilter{}, nil, ErrInvalidMyOrdersFilter
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = booking.DefaultMyOrdersLimit
	case filter.Limit < 1 || filter.Limit > booking.MaxMyOrdersLimit:
		return booking.MyOrderFilter{}, nil, ErrInvalidMyOrdersFilter
	}
	requestedAt := filter.At
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	filter.At = requestedAt.UTC()
	if filter.Cursor == "" {
		return filter, nil, nil
	}
	cursor, err := decodeMyOrdersCursor(filter.Cursor)
	if err != nil {
		return booking.MyOrderFilter{}, nil, err
	}
	if cursor.TenantID != filter.TenantID ||
		cursor.PrincipalID != filter.PrincipalID ||
		cursor.FilterState != filter.State {
		return booking.MyOrderFilter{}, nil, ErrStaleMyOrdersCursor
	}
	if filter.At.Sub(cursor.AsOf) > booking.MaxMyOrdersCursorAge ||
		cursor.AsOf.After(filter.At.Add(booking.MyOrdersFutureSkew)) {
		return booking.MyOrderFilter{}, nil, ErrStaleMyOrdersCursor
	}
	filter.At = cursor.AsOf.UTC()
	return filter, &cursor, nil
}

func encodeMyOrdersCursor(
	filter booking.MyOrderFilter,
	item booking.MyOrderItem,
) (string, error) {
	encoded, err := json.Marshal(decodedMyOrdersCursor{
		Version:     1,
		TenantID:    filter.TenantID,
		PrincipalID: filter.PrincipalID,
		FilterState: filter.State,
		AsOf:        filter.At.UTC(),
		ItemState:   item.State,
		SortRank:    item.SortRank,
		SortAt:      item.SortAt.UTC(),
		OrderID:     item.OrderID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode", ErrInvalidMyOrdersCursor)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMyOrdersCursor(value string) (decodedMyOrdersCursor, error) {
	if len(value) > 2048 {
		return decodedMyOrdersCursor{}, fmt.Errorf(
			"%w: payload is too large",
			ErrInvalidMyOrdersCursor,
		)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return decodedMyOrdersCursor{}, fmt.Errorf(
			"%w: malformed base64",
			ErrInvalidMyOrdersCursor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor decodedMyOrdersCursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedMyOrdersCursor{}, fmt.Errorf(
			"%w: malformed payload",
			ErrInvalidMyOrdersCursor,
		)
	}
	if err := ensureMyOrdersCursorEOF(decoder); err != nil {
		return decodedMyOrdersCursor{}, err
	}
	if cursor.Version != 1 ||
		cursor.TenantID == uuid.Nil ||
		cursor.PrincipalID == uuid.Nil ||
		!booking.ValidMyOrderState(cursor.FilterState) ||
		!booking.ValidMyOrderState(cursor.ItemState) ||
		cursor.ItemState == booking.MyOrderStateAll ||
		cursor.AsOf.IsZero() ||
		cursor.SortRank != booking.MyOrderStateSortRank(cursor.ItemState) ||
		cursor.SortAt.IsZero() ||
		cursor.OrderID == uuid.Nil ||
		(cursor.FilterState != booking.MyOrderStateAll &&
			cursor.FilterState != cursor.ItemState) {
		return decodedMyOrdersCursor{}, fmt.Errorf(
			"%w: invalid fields",
			ErrInvalidMyOrdersCursor,
		)
	}
	cursor.AsOf = cursor.AsOf.UTC()
	cursor.SortAt = cursor.SortAt.UTC()
	return cursor, nil
}

func ensureMyOrdersCursorEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing payload", ErrInvalidMyOrdersCursor)
	}
	return nil
}
