package bookingpostgres

import (
	"context"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyOrderIdentity = booking.ErrInvalidMyOrderIdentity
	ErrMyOrderNotFound        = booking.ErrMyOrderNotFound
)

func (repository *Repository) GetOrderDetail(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	orderID uuid.UUID,
	at time.Time,
) (booking.MyOrderDetail, error) {
	if tenantID == uuid.Nil ||
		principalID == uuid.Nil ||
		orderID == uuid.Nil {
		return booking.MyOrderDetail{}, ErrInvalidMyOrderIdentity
	}
	if at.IsZero() {
		at = time.Now()
	}
	asOf := at.UTC()
	query := myRegistrationsQueryPrefix +
		myOrderProjection + `
FROM ranked
WHERE tenant_id = $1
  AND principal_id = $2
  AND order_id = $4
LIMIT 2
`
	rows, err := repository.db.queryContext(
		ctx,
		query,
		tenantID,
		principalID,
		asOf,
		orderID,
	)
	if err != nil {
		return booking.MyOrderDetail{}, fmt.Errorf(
			"get xiangwan My Order detail: %w",
			err,
		)
	}
	defer func() {
		_ = rows.Close()
	}()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return booking.MyOrderDetail{}, fmt.Errorf(
				"iterate xiangwan My Order detail: %w",
				err,
			)
		}
		return booking.MyOrderDetail{}, ErrMyOrderNotFound
	}
	item, err := scanMyOrderItem(rows, asOf)
	if err != nil {
		return booking.MyOrderDetail{}, fmt.Errorf(
			"scan xiangwan My Order detail: %w",
			err,
		)
	}
	if rows.Next() {
		return booking.MyOrderDetail{}, fmt.Errorf(
			"%w: duplicate Order identity",
			ErrMyOrderProjection,
		)
	}
	if err := rows.Err(); err != nil {
		return booking.MyOrderDetail{}, fmt.Errorf(
			"iterate xiangwan My Order detail: %w",
			err,
		)
	}
	return booking.MyOrderDetail{Item: item, AsOf: asOf}, nil
}
