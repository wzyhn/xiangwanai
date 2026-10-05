package registrationpostgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestCancellationSQLTransactionDelegatesEveryPersistenceOperation(t *testing.T) {
	registerCancellationSQLStubDriver()
	now := time.Date(2026, time.September, 12, 11, 0, 0, 0, time.UTC)
	current, order, hold := paidCancellationFixture(t, now.Add(-time.Hour), true)
	cancelled, _, err := registration.CancelRegistration(
		current,
		"user_cancelled@cancel-v3",
		now,
	)
	if err != nil {
		t.Fatalf("CancelRegistration() error = %v", err)
	}
	refundCase, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             order.TenantID,
		OrderID:              order.ID,
		RegistrationID:       order.RegistrationID,
		SeriesID:             order.SeriesID,
		InstanceID:           order.InstanceID,
		SessionID:            order.SessionID,
		PrincipalID:          order.PrincipalID,
		ReasonCode:           refund.ReasonUserCancelled,
		IdempotencyKey:       "refund:cancellation:" + current.ID.String(),
		ActualPaidCents:      *order.ActualPaidCents,
		RequestedRefundCents: *order.ActualPaidCents,
		Now:                  now,
	})
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}

	state := &cancellationSQLStubState{
		registration: cancelled,
		order:        order,
		hold:         hold,
		refundCase:   refundCase,
		sessionStart: now.Add(2 * time.Hour),
	}
	cancellationSQLStubCurrent = state
	db, err := sql.Open(cancellationSQLStubDriverName, "happy-path")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	constructed := NewRegistrationCanceller(db, nil)
	if constructed.registrations == nil || constructed.transactions == nil || constructed.now == nil {
		t.Fatalf("NewRegistrationCanceller() = %+v", constructed)
	}

	tx, err := (cancellationSQLTransactionStarter{db: db}).beginCancellationTx(
		context.Background(),
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		t.Fatalf("beginCancellationTx() error = %v", err)
	}
	ctx := context.Background()
	if err := tx.lockSeries(ctx, current.TenantID, current.SeriesID); err != nil {
		t.Fatalf("lockSeries() error = %v", err)
	}
	if err := tx.lockInstance(ctx, current.TenantID, current.SeriesID, current.InstanceID); err != nil {
		t.Fatalf("lockInstance() error = %v", err)
	}
	lockedSession, err := tx.lockSession(ctx, current.TenantID, current.InstanceID, current.SessionID)
	if err != nil || lockedSession.version != 9 || lockedSession.sessionStartAt != state.sessionStart {
		t.Fatalf("lockSession() = %+v, %v", lockedSession, err)
	}
	lockedRegistration, err := tx.lockRegistration(ctx, current.TenantID, current.ID)
	if err != nil || !reflect.DeepEqual(lockedRegistration, cancelled) {
		t.Fatalf("lockRegistration() = %+v, %v", lockedRegistration, err)
	}
	lockedOrder, err := tx.lockOrderByRegistration(ctx, current.TenantID, current.ID)
	if err != nil || !reflect.DeepEqual(lockedOrder, order) {
		t.Fatalf("lockOrderByRegistration() = %+v, %v", lockedOrder, err)
	}
	lockedHold, err := tx.lockHoldByOrder(ctx, current.TenantID, order.ID)
	if err != nil || !reflect.DeepEqual(lockedHold, hold) {
		t.Fatalf("lockHoldByOrder() = %+v, %v", lockedHold, err)
	}
	lockedRefund, err := tx.lockRefundByOrder(ctx, current.TenantID, order.ID)
	if err != nil || !reflect.DeepEqual(lockedRefund, refundCase) {
		t.Fatalf("lockRefundByOrder() = %+v, %v", lockedRefund, err)
	}
	if _, err := tx.updateRegistration(ctx, cancelled, current.Version); err != nil {
		t.Fatalf("updateRegistration() error = %v", err)
	}
	if _, err := tx.updateOrder(ctx, order, order.Version-1); err != nil {
		t.Fatalf("updateOrder() error = %v", err)
	}
	if _, err := tx.updateHold(ctx, hold, hold.Version-1); err != nil {
		t.Fatalf("updateHold() error = %v", err)
	}
	if _, err := tx.createRefund(ctx, refundCase); err != nil {
		t.Fatalf("createRefund() error = %v", err)
	}
	if err := tx.decrementConfirmedCapacity(
		ctx,
		current.TenantID,
		current.InstanceID,
		current.SessionID,
		9,
		now,
	); err != nil {
		t.Fatalf("decrementConfirmedCapacity() error = %v", err)
	}
	if err := tx.decrementHoldCapacity(
		ctx,
		current.TenantID,
		current.InstanceID,
		current.SessionID,
		9,
		now,
	); err != nil {
		t.Fatalf("decrementHoldCapacity() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if state.commits != 1 || state.queries < 12 {
		t.Fatalf("stub state after commit = %+v", state)
	}

	rollbackTx, err := (cancellationSQLTransactionStarter{db: db}).beginCancellationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		t.Fatalf("beginCancellationTx(rollback) error = %v", err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if state.rollbacks != 1 {
		t.Fatalf("rollback count = %d", state.rollbacks)
	}
}

func TestCancellationSQLHelpersFailClosed(t *testing.T) {
	t.Parallel()

	if err := cancellationLockError("Session", nil); err != nil {
		t.Fatalf("cancellationLockError(nil) = %v", err)
	}
	if err := cancellationLockError("Session", sql.ErrNoRows); !errors.Is(
		err,
		ErrRegistrationCancellationTransaction,
	) {
		t.Fatalf("cancellationLockError(no rows) = %v", err)
	}
	queryFailure := errors.New("query failed")
	if err := cancellationLockError("Session", queryFailure); !errors.Is(err, queryFailure) {
		t.Fatalf("cancellationLockError(query) = %v", err)
	}
	if err := (&cancellationSQLTransaction{}).decrementCapacity(
		context.Background(),
		"untrusted_column",
		uuid.New(),
		uuid.New(),
		uuid.New(),
		1,
		time.Now(),
	); !errors.Is(err, ErrRegistrationCancellationTransaction) {
		t.Fatalf("decrementCapacity(untrusted column) = %v", err)
	}
}

const cancellationSQLStubDriverName = "xiangwan-cancellation-postgres-stub"

var (
	registerCancellationSQLStubOnce sync.Once
	cancellationSQLStubCurrent      *cancellationSQLStubState
)

func registerCancellationSQLStubDriver() {
	registerCancellationSQLStubOnce.Do(func() {
		sql.Register(cancellationSQLStubDriverName, cancellationSQLStubDriver{})
	})
}

type cancellationSQLStubState struct {
	registration registration.Registration
	order        payment.Order
	hold         payment.CapacityHold
	refundCase   refund.Case
	sessionStart time.Time
	queries      int
	commits      int
	rollbacks    int
}

type cancellationSQLStubDriver struct{}

func (cancellationSQLStubDriver) Open(string) (driver.Conn, error) {
	return &cancellationSQLStubConnection{state: cancellationSQLStubCurrent}, nil
}

type cancellationSQLStubConnection struct {
	state *cancellationSQLStubState
}

func (connection *cancellationSQLStubConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported")
}

func (connection *cancellationSQLStubConnection) Close() error {
	return nil
}

func (connection *cancellationSQLStubConnection) Begin() (driver.Tx, error) {
	return &cancellationSQLStubTransaction{state: connection.state}, nil
}

func (connection *cancellationSQLStubConnection) BeginTx(
	context.Context,
	driver.TxOptions,
) (driver.Tx, error) {
	return &cancellationSQLStubTransaction{state: connection.state}, nil
}

func (connection *cancellationSQLStubConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	connection.state.queries++
	switch {
	case strings.Contains(query, "FROM xiangwan_activity_series"):
		return cancellationStubRows(int64(7)), nil
	case strings.Contains(query, "FROM xiangwan_activity_instances"):
		return cancellationStubRows(int64(8)), nil
	case strings.Contains(query, "FROM xiangwan_activity_sessions"):
		return cancellationStubRows(connection.state.sessionStart, int64(9)), nil
	case strings.Contains(query, "UPDATE xiangwan_activity_sessions"):
		return cancellationStubRows(int64(10)), nil
	case strings.Contains(query, "xiangwan_registrations"):
		return cancellationStubRows(registrationDriverValues(connection.state.registration)...), nil
	case strings.Contains(query, "xiangwan_orders"):
		return cancellationStubRows(orderDriverValues(connection.state.order)...), nil
	case strings.Contains(query, "xiangwan_capacity_holds"):
		return cancellationStubRows(holdDriverValues(connection.state.hold)...), nil
	case strings.Contains(query, "xiangwan_refund_cases"):
		return cancellationStubRows(refundDriverValues(connection.state.refundCase)...), nil
	default:
		return nil, errors.New("unexpected cancellation SQL")
	}
}

type cancellationSQLStubTransaction struct {
	state *cancellationSQLStubState
}

func (transaction *cancellationSQLStubTransaction) Commit() error {
	transaction.state.commits++
	return nil
}

func (transaction *cancellationSQLStubTransaction) Rollback() error {
	transaction.state.rollbacks++
	return nil
}

type cancellationSQLStubRows struct {
	values []driver.Value
	read   bool
}

func cancellationStubRows(values ...driver.Value) *cancellationSQLStubRows {
	return &cancellationSQLStubRows{values: values}
}

func (rows *cancellationSQLStubRows) Columns() []string {
	columns := make([]string, len(rows.values))
	for index := range columns {
		columns[index] = "value"
	}
	return columns
}

func (rows *cancellationSQLStubRows) Close() error {
	return nil
}

func (rows *cancellationSQLStubRows) Next(destinations []driver.Value) error {
	if rows.read {
		return io.EOF
	}
	rows.read = true
	copy(destinations, rows.values)
	return nil
}

func registrationDriverValues(value registration.Registration) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.SessionID.String(),
		value.PrincipalID.String(),
		string(value.ParticipationStatus),
		value.IdempotencyKey,
		driverTime(value.ConfirmedAt),
		driverTime(value.CancelledAt),
		driverString(value.CancellationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func orderDriverValues(value payment.Order) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.RegistrationID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.SessionID.String(),
		value.PrincipalID.String(),
		string(value.PaymentStatus),
		value.IdempotencyKey,
		value.MerchantOrderNo,
		value.PaymentAppID,
		value.PaymentMerchantID,
		driverUUIDValueOrNil(value.MerchantConfigGenerationID),
		value.OriginalPriceCents,
		value.DiscountCents,
		value.PayableCents,
		driverInt64(value.ActualPaidCents),
		driverString(value.WeChatTransactionID),
		driverTime(value.PaidAt),
		driverTime(value.ClosedAt),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func driverUUIDValueOrNil(value uuid.UUID) driver.Value {
	if value == uuid.Nil {
		return nil
	}
	return value.String()
}

func holdDriverValues(value payment.CapacityHold) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.OrderID.String(),
		value.RegistrationID.String(),
		value.SessionID.String(),
		string(value.HoldStatus),
		value.ExpiresAt,
		driverTime(value.ConvertedAt),
		driverTime(value.ReleasedAt),
		driverString(value.ReleaseReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func refundDriverValues(value refund.Case) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.OrderID.String(),
		value.RegistrationID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.SessionID.String(),
		value.PrincipalID.String(),
		string(value.RefundStatus),
		string(value.ReasonCode),
		value.IdempotencyKey,
		value.RequestedRefundCents,
		value.SuccessfulRefundCents,
		driverTime(value.ProcessingStartedAt),
		driverTime(value.ResolvedAt),
		driverUUID(value.HandledBy),
		driverString(value.ExternalRefundID),
		driverString(value.EvidenceReference),
		driverString(value.OperatorNote),
		driverString(value.FailureReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func driverTime(value *time.Time) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func driverString(value *string) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func driverInt64(value *int64) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func driverUUID(value *uuid.UUID) driver.Value {
	if value == nil {
		return nil
	}
	return value.String()
}
