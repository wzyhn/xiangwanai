package refundpostgres

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
	"github.com/google/uuid"
)

func TestRefundOperationSQLTransactionDelegatesPersistence(t *testing.T) {
	registerRefundOperationSQLStubDriver()
	refundCase := pendingRefundCase(t)
	order := paidOrderForRefundCase(refundCase)
	event := completedRefundEvent(t)
	event.TenantID = refundCase.TenantID
	event.RefundCaseID = refundCase.ID
	event.OrderID = refundCase.OrderID
	state := &refundOperationSQLStubState{
		refundCase: refundCase,
		order:      order,
		event:      event,
	}
	refundOperationSQLStubCurrent = state
	db, err := sql.Open(refundOperationSQLStubDriverName, "happy-path")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	constructed := NewProcessor(db)
	if constructed.transactions == nil || constructed.now == nil {
		t.Fatalf("NewProcessor() = %+v", constructed)
	}
	tx, err := (refundOperationSQLTransactionStarter{db: db}).beginRefundOperationTx(
		context.Background(),
		&sql.TxOptions{Isolation: sql.LevelSerializable},
		uuid.Nil, uuid.Nil, "start",
	)
	if err != nil {
		t.Fatalf("beginRefundOperationTx() error = %v", err)
	}
	ctx := context.Background()
	locatedRefund, err := tx.locateRefund(ctx, refundCase.TenantID, refundCase.ID)
	if err != nil || !reflect.DeepEqual(locatedRefund, refundCase) {
		t.Fatalf("locateRefund() = %+v, %v", locatedRefund, err)
	}
	lockedOrder, err := tx.lockOrder(ctx, refundCase.TenantID, refundCase.OrderID)
	if err != nil || !reflect.DeepEqual(lockedOrder, order) {
		t.Fatalf("lockOrder() = %+v, %v", lockedOrder, err)
	}
	lockedRefund, err := tx.lockRefund(ctx, refundCase.TenantID, refundCase.ID)
	if err != nil || !reflect.DeepEqual(lockedRefund, refundCase) {
		t.Fatalf("lockRefund() = %+v, %v", lockedRefund, err)
	}
	storedEvent, err := tx.getEventByIdempotencyKey(
		ctx,
		refundCase.TenantID,
		refundCase.ID,
		event.IdempotencyKey,
	)
	if err != nil || !reflect.DeepEqual(storedEvent, event) {
		t.Fatalf("getEventByIdempotencyKey() = %+v, %v", storedEvent, err)
	}
	if _, err := tx.updateRefund(ctx, refundCase, refundCase.Version); err != nil {
		t.Fatalf("updateRefund() error = %v", err)
	}
	if _, err := tx.createEvent(ctx, event); err != nil {
		t.Fatalf("createEvent() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if state.commits != 1 || state.queries != 6 {
		t.Fatalf("stub state = %+v", state)
	}

	rollbackTx, err := (refundOperationSQLTransactionStarter{db: db}).beginRefundOperationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
		uuid.Nil, uuid.Nil, "start",
	)
	if err != nil {
		t.Fatalf("beginRefundOperationTx(rollback) error = %v", err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if state.rollbacks != 1 {
		t.Fatalf("rollback count = %d", state.rollbacks)
	}
}

func TestAuthorizedRefundProcessorDeniesBeforeCaseLookupAndRollsBack(t *testing.T) {
	registerRefundOperationSQLStubDriver()
	state := &refundOperationSQLStubState{}
	refundOperationSQLStubCurrent = state
	db, err := sql.Open(refundOperationSQLStubDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	denied := errors.New("live Grant denied")
	actorID, identityID := uuid.New(), uuid.New()
	called := false
	processor := NewAuthorizedProcessorWithCouponRefundPolicy(db, nil,
		func(_ context.Context, _ *sql.Tx, actor, identity uuid.UUID, action string) error {
			called = actor == actorID && identity == identityID && action == "start"
			return denied
		})
	_, err = processor.StartProcessing(context.Background(), StartProcessingCommand{
		TenantID: uuid.New(), RefundCaseID: uuid.New(), ActorID: actorID,
		IdentityLinkID: identityID, IdempotencyKey: uuid.NewString(),
		OccurredAt: time.Now(),
	})
	if !errors.Is(err, denied) || !called || state.queries != 0 ||
		state.commits != 0 || state.rollbacks != 1 {
		t.Fatalf("authorization result = %v called=%t state=%+v", err, called, state)
	}
}

const refundOperationSQLStubDriverName = "xiangwan-refund-operation-postgres-stub"

var (
	registerRefundOperationSQLStubOnce sync.Once
	refundOperationSQLStubCurrent      *refundOperationSQLStubState
)

func registerRefundOperationSQLStubDriver() {
	registerRefundOperationSQLStubOnce.Do(func() {
		sql.Register(refundOperationSQLStubDriverName, refundOperationSQLStubDriver{})
	})
}

type refundOperationSQLStubState struct {
	refundCase refund.Case
	order      payment.Order
	event      refund.Event
	queries    int
	commits    int
	rollbacks  int
}

type refundOperationSQLStubDriver struct{}

func (refundOperationSQLStubDriver) Open(string) (driver.Conn, error) {
	return &refundOperationSQLStubConnection{state: refundOperationSQLStubCurrent}, nil
}

type refundOperationSQLStubConnection struct {
	state *refundOperationSQLStubState
}

func (connection *refundOperationSQLStubConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported")
}

func (connection *refundOperationSQLStubConnection) Close() error {
	return nil
}

func (connection *refundOperationSQLStubConnection) Begin() (driver.Tx, error) {
	return &refundOperationSQLStubTransaction{state: connection.state}, nil
}

func (connection *refundOperationSQLStubConnection) BeginTx(
	context.Context,
	driver.TxOptions,
) (driver.Tx, error) {
	return &refundOperationSQLStubTransaction{state: connection.state}, nil
}

func (connection *refundOperationSQLStubConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	connection.state.queries++
	switch {
	case strings.Contains(query, "xiangwan_refund_events"):
		return refundOperationStubRows(refundEventDriverValues(connection.state.event)...), nil
	case strings.Contains(query, "xiangwan_refund_cases"):
		return refundOperationStubRows(refundCaseDriverValues(connection.state.refundCase)...), nil
	case strings.Contains(query, "xiangwan_orders"):
		return refundOperationStubRows(refundOrderDriverValues(connection.state.order)...), nil
	default:
		return nil, errors.New("unexpected Refund operation SQL")
	}
}

type refundOperationSQLStubTransaction struct {
	state *refundOperationSQLStubState
}

func (transaction *refundOperationSQLStubTransaction) Commit() error {
	transaction.state.commits++
	return nil
}

func (transaction *refundOperationSQLStubTransaction) Rollback() error {
	transaction.state.rollbacks++
	return nil
}

type refundOperationSQLStubRows struct {
	values []driver.Value
	read   bool
}

func refundOperationStubRows(values ...driver.Value) *refundOperationSQLStubRows {
	return &refundOperationSQLStubRows{values: values}
}

func (rows *refundOperationSQLStubRows) Columns() []string {
	columns := make([]string, len(rows.values))
	for index := range columns {
		columns[index] = "value"
	}
	return columns
}

func (rows *refundOperationSQLStubRows) Close() error {
	return nil
}

func (rows *refundOperationSQLStubRows) Next(destinations []driver.Value) error {
	if rows.read {
		return io.EOF
	}
	rows.read = true
	copy(destinations, rows.values)
	return nil
}

func refundCaseDriverValues(value refund.Case) []driver.Value {
	return []driver.Value{
		value.ID.String(), value.TenantID.String(), value.OrderID.String(),
		value.RegistrationID.String(), value.SeriesID.String(), value.InstanceID.String(),
		value.SessionID.String(), value.PrincipalID.String(), string(value.RefundStatus),
		string(value.ReasonCode), value.IdempotencyKey, value.RequestedRefundCents,
		value.SuccessfulRefundCents, refundDriverTime(value.ProcessingStartedAt),
		refundDriverTime(value.ResolvedAt), refundDriverUUID(value.HandledBy),
		refundDriverString(value.ExternalRefundID), refundDriverString(value.EvidenceReference),
		refundDriverString(value.OperatorNote), refundDriverString(value.FailureReason),
		value.Version, value.CreatedAt, value.UpdatedAt,
	}
}

func refundOrderDriverValues(value payment.Order) []driver.Value {
	return []driver.Value{
		value.ID.String(), value.TenantID.String(), value.RegistrationID.String(),
		value.SeriesID.String(), value.InstanceID.String(), value.SessionID.String(),
		value.PrincipalID.String(), string(value.PaymentStatus), value.IdempotencyKey,
		value.MerchantOrderNo, value.PaymentAppID, value.PaymentMerchantID,
		refundDriverUUIDValue(value.MerchantConfigGenerationID),
		value.OriginalPriceCents, value.DiscountCents, value.PayableCents,
		refundDriverInt64(value.ActualPaidCents), refundDriverString(value.WeChatTransactionID),
		refundDriverTime(value.PaidAt), refundDriverTime(value.ClosedAt),
		value.Version, value.CreatedAt, value.UpdatedAt,
	}
}

func refundDriverUUIDValue(value uuid.UUID) driver.Value {
	if value == uuid.Nil {
		return nil
	}
	return value.String()
}

func refundEventDriverValues(value refund.Event) []driver.Value {
	return []driver.Value{
		value.ID.String(), value.TenantID.String(), value.RefundCaseID.String(),
		value.OrderID.String(), value.EventSequence, string(value.EventType),
		value.IdempotencyKey, string(value.FromStatus), string(value.ToStatus),
		value.ActorID.String(), value.SuccessfulRefundCents,
		refundDriverString(value.ExternalRefundID), refundDriverString(value.EvidenceReference),
		refundDriverString(value.OperatorNote), refundDriverString(value.FailureReason),
		value.OccurredAt, value.ResultingRefundVersion, value.CreatedAt,
	}
}

func refundDriverTime(value *time.Time) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func refundDriverString(value *string) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func refundDriverInt64(value *int64) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func refundDriverUUID(value *uuid.UUID) driver.Value {
	if value == nil {
		return nil
	}
	return value.String()
}
