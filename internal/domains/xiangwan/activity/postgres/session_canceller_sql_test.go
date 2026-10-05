package activitypostgres

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

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestSessionCancellationSQLAdapterDelegatesEveryPersistenceStep(t *testing.T) {
	registerSessionCancellationSQLStub()
	fixture := newSessionCancellationFixture(t)
	order := fixture.paidOrder
	hold := fixture.holds[order.ID]
	refundCase, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             order.TenantID,
		OrderID:              order.ID,
		RegistrationID:       order.RegistrationID,
		SeriesID:             order.SeriesID,
		InstanceID:           order.InstanceID,
		SessionID:            order.SessionID,
		PrincipalID:          order.PrincipalID,
		ReasonCode:           refund.ReasonSessionCancelled,
		IdempotencyKey:       "refund:session-cancel:sql",
		ActualPaidCents:      *order.ActualPaidCents,
		RequestedRefundCents: *order.ActualPaidCents,
		Now:                  fixture.processedAt,
	})
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	receipt := activity.SessionCancellationReceipt{
		ID:                         uuid.New(),
		TenantID:                   fixture.target.TenantID,
		SeriesID:                   fixture.target.SeriesID,
		InstanceID:                 fixture.target.InstanceID,
		SessionID:                  fixture.target.SessionID,
		PreviewID:                  fixture.preview.ID,
		IdempotencyKey:             fixture.command.IdempotencyKey,
		CancelledBy:                fixture.command.ActorID,
		CancellationReason:         fixture.command.Reason,
		NotificationStrategy:       fixture.command.NotificationStrategy,
		CancelledRegistrationCount: 1,
		ReleasedConfirmedCount:     1,
		RefundCaseCount:            1,
		RequestedRefundCents:       *order.ActualPaidCents,
		CancelledAt:                fixture.processedAt,
		ResultingSessionVersion:    fixture.session.Version + 1,
		CreatedAt:                  fixture.processedAt,
	}
	state := &sessionCancellationSQLStubState{
		target:         fixture.target,
		seriesStatus:   fixture.seriesStatus,
		instanceStatus: fixture.instanceStatus,
		session:        fixture.session,
		receipt:        receipt,
		registration:   fixture.paidRegistration,
		order:          order,
		hold:           hold,
		refundCase:     refundCase,
		preview:        fixture.preview,
	}
	sessionCancellationSQLStubCurrent = state
	db, err := sql.Open(sessionCancellationSQLStubDriverName, "adapter")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	constructed := NewSessionCanceller(db)
	if constructed.resolver == nil ||
		constructed.transactions == nil ||
		constructed.now == nil {
		t.Fatalf("NewSessionCanceller() = %+v", constructed)
	}
	constructedPreviewer := NewSessionCancellationPreviewer(db)
	if constructedPreviewer.resolver == nil ||
		constructedPreviewer.transactions == nil ||
		constructedPreviewer.now == nil {
		t.Fatalf("NewSessionCancellationPreviewer() = %+v", constructedPreviewer)
	}
	ctx := context.Background()
	target, err := (sessionCancellationSQLResolver{db: db}).
		resolveSessionCancellationTarget(
			ctx,
			fixture.target.TenantID,
			fixture.target.SessionID,
		)
	if err != nil || target != fixture.target {
		t.Fatalf("resolveSessionCancellationTarget() = %+v, %v", target, err)
	}
	tx, err := (sessionCancellationSQLTransactionStarter{db: db}).
		beginSessionCancellationTx(
			ctx,
			&sql.TxOptions{Isolation: sql.LevelSerializable},
		)
	if err != nil {
		t.Fatalf("beginSessionCancellationTx() error = %v", err)
	}
	seriesStatus, err := tx.lockSeries(
		ctx,
		fixture.target.TenantID,
		fixture.target.SeriesID,
	)
	if err != nil || seriesStatus != fixture.seriesStatus {
		t.Fatalf("lockSeries() = %q, %v", seriesStatus, err)
	}
	instanceStatus, err := tx.lockInstance(
		ctx,
		fixture.target.TenantID,
		fixture.target.SeriesID,
		fixture.target.InstanceID,
	)
	if err != nil || instanceStatus != fixture.instanceStatus {
		t.Fatalf("lockInstance() = %q, %v", instanceStatus, err)
	}
	session, err := tx.lockSession(
		ctx,
		fixture.target.TenantID,
		fixture.target.InstanceID,
		fixture.target.SessionID,
	)
	if err != nil || !reflect.DeepEqual(session, fixture.session) {
		t.Fatalf("lockSession() = %+v, %v", session, err)
	}
	storedReceipt, err := tx.getReceiptBySession(
		ctx,
		fixture.target.TenantID,
		fixture.target.SessionID,
	)
	if err != nil || storedReceipt != receipt {
		t.Fatalf("getReceiptBySession() = %+v, %v", storedReceipt, err)
	}
	storedPreview, err := tx.getPreviewByKey(
		ctx,
		fixture.preview.TenantID,
		fixture.preview.IdempotencyKey,
	)
	if err != nil || !reflect.DeepEqual(storedPreview, fixture.preview) {
		t.Fatalf("getPreviewByKey() = %+v, %v", storedPreview, err)
	}
	lockedPreview, err := tx.lockPreview(
		ctx,
		fixture.preview.TenantID,
		fixture.preview.ID,
	)
	if err != nil || !reflect.DeepEqual(lockedPreview, fixture.preview) {
		t.Fatalf("lockPreview() = %+v, %v", lockedPreview, err)
	}
	registrations, err := tx.listOpenRegistrations(
		ctx,
		fixture.target.TenantID,
		fixture.target.SessionID,
	)
	if err != nil ||
		len(registrations) != 1 ||
		!reflect.DeepEqual(registrations[0], fixture.paidRegistration) {
		t.Fatalf("listOpenRegistrations() = %+v, %v", registrations, err)
	}
	storedOrder, found, err := tx.lockOrderByRegistration(
		ctx,
		order.TenantID,
		order.RegistrationID,
	)
	if err != nil || !found || !reflect.DeepEqual(storedOrder, order) {
		t.Fatalf("lockOrderByRegistration() = %+v, %t, %v", storedOrder, found, err)
	}
	storedHold, err := tx.lockHoldByOrder(ctx, hold.TenantID, hold.OrderID)
	if err != nil || !reflect.DeepEqual(storedHold, hold) {
		t.Fatalf("lockHoldByOrder() = %+v, %v", storedHold, err)
	}
	storedRefund, err := tx.lockRefundByOrder(
		ctx,
		refundCase.TenantID,
		refundCase.OrderID,
	)
	if err != nil || !reflect.DeepEqual(storedRefund, refundCase) {
		t.Fatalf("lockRefundByOrder() = %+v, %v", storedRefund, err)
	}
	if _, err := tx.updateRegistration(
		ctx,
		fixture.paidRegistration,
		fixture.paidRegistration.Version,
	); err != nil {
		t.Fatalf("updateRegistration() error = %v", err)
	}
	if _, err := tx.updateOrder(ctx, order, order.Version); err != nil {
		t.Fatalf("updateOrder() error = %v", err)
	}
	if _, err := tx.updateHold(ctx, hold, hold.Version); err != nil {
		t.Fatalf("updateHold() error = %v", err)
	}
	if _, err := tx.createRefund(ctx, refundCase); err != nil {
		t.Fatalf("createRefund() error = %v", err)
	}
	if _, err := tx.updateSession(
		ctx,
		fixture.session,
		fixture.session.Version,
	); err != nil {
		t.Fatalf("updateSession() error = %v", err)
	}
	if _, err := tx.createReceipt(ctx, receipt); err != nil {
		t.Fatalf("createReceipt() error = %v", err)
	}
	if _, err := tx.createPreview(ctx, fixture.preview); err != nil {
		t.Fatalf("createPreview() error = %v", err)
	}
	if _, err := tx.consumePreview(
		ctx,
		fixture.preview.TenantID,
		fixture.preview.ID,
		fixture.processedAt,
	); err != nil {
		t.Fatalf("consumePreview() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	rollbackTx, err := (sessionCancellationSQLTransactionStarter{db: db}).
		beginSessionCancellationTx(
			ctx,
			&sql.TxOptions{Isolation: sql.LevelSerializable},
		)
	if err != nil {
		t.Fatalf("beginSessionCancellationTx(rollback) error = %v", err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if state.begins != 2 ||
		state.commits != 1 ||
		state.rollbacks != 1 ||
		state.queries != 19 {
		t.Fatalf("SQL adapter state = %+v", state)
	}
	for _, fragment := range []string{
		"activity_session.tenant_id = $1",
		"FROM xiangwan_activity_series",
		"FROM xiangwan_activity_instances",
		"FROM xiangwan_activity_sessions",
		"FROM xiangwan_registrations",
		"ORDER BY id",
		"FOR UPDATE",
		"UPDATE xiangwan_activity_sessions",
		"INSERT INTO xiangwan_session_cancellation_receipts",
		"FROM xiangwan_session_cancellation_previews",
		"INSERT INTO xiangwan_session_cancellation_previews",
		"UPDATE xiangwan_session_cancellation_previews",
	} {
		if !queriesContain(state.queryTexts, fragment) {
			t.Fatalf("SQL adapter queries do not contain %q: %v", fragment, state.queryTexts)
		}
	}
}

const sessionCancellationSQLStubDriverName = "xiangwan-session-cancellation-postgres-stub"

var (
	registerSessionCancellationSQLStubOnce sync.Once
	sessionCancellationSQLStubCurrent      *sessionCancellationSQLStubState
)

func registerSessionCancellationSQLStub() {
	registerSessionCancellationSQLStubOnce.Do(func() {
		sql.Register(
			sessionCancellationSQLStubDriverName,
			sessionCancellationSQLStubDriver{},
		)
	})
}

type sessionCancellationSQLStubState struct {
	target         sessionCancellationTarget
	seriesStatus   activity.SeriesStatus
	instanceStatus activity.InstanceStatus
	session        activity.Session
	receipt        activity.SessionCancellationReceipt
	registration   registration.Registration
	order          payment.Order
	hold           payment.CapacityHold
	refundCase     refund.Case
	preview        activity.SessionCancellationPreview
	queryTexts     []string
	queries        int
	begins         int
	commits        int
	rollbacks      int
}

type sessionCancellationSQLStubDriver struct{}

func (sessionCancellationSQLStubDriver) Open(string) (driver.Conn, error) {
	return &sessionCancellationSQLStubConnection{
		state: sessionCancellationSQLStubCurrent,
	}, nil
}

type sessionCancellationSQLStubConnection struct {
	state *sessionCancellationSQLStubState
}

func (connection *sessionCancellationSQLStubConnection) Prepare(
	string,
) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported")
}

func (connection *sessionCancellationSQLStubConnection) Close() error {
	return nil
}

func (connection *sessionCancellationSQLStubConnection) Begin() (driver.Tx, error) {
	connection.state.begins++
	return &sessionCancellationSQLStubTransaction{state: connection.state}, nil
}

func (connection *sessionCancellationSQLStubConnection) BeginTx(
	context.Context,
	driver.TxOptions,
) (driver.Tx, error) {
	connection.state.begins++
	return &sessionCancellationSQLStubTransaction{state: connection.state}, nil
}

func (connection *sessionCancellationSQLStubConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	state := connection.state
	state.queries++
	state.queryTexts = append(state.queryTexts, query)
	switch {
	case strings.Contains(query, "activity_series.tenant_id") &&
		strings.Contains(query, "activity_session.id"):
		return newSessionCancellationSQLStubRows(
			state.target.TenantID.String(),
			state.target.SeriesID.String(),
			state.target.InstanceID.String(),
			state.target.SessionID.String(),
		), nil
	case strings.Contains(query, "FROM xiangwan_activity_series"):
		return newSessionCancellationSQLStubRows(string(state.seriesStatus)), nil
	case strings.Contains(query, "FROM xiangwan_activity_instances"):
		return newSessionCancellationSQLStubRows(string(state.instanceStatus)), nil
	case strings.Contains(query, "xiangwan_activity_sessions"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationSessionDriverValues(state.session)...,
		), nil
	case strings.Contains(query, "xiangwan_session_cancellation_receipts"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationReceiptDriverValues(state.receipt)...,
		), nil
	case strings.Contains(query, "xiangwan_session_cancellation_previews"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationPreviewDriverValues(state.preview)...,
		), nil
	case strings.Contains(query, "xiangwan_registrations"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationRegistrationDriverValues(state.registration)...,
		), nil
	case strings.Contains(query, "xiangwan_orders"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationOrderDriverValues(state.order)...,
		), nil
	case strings.Contains(query, "xiangwan_capacity_holds"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationHoldDriverValues(state.hold)...,
		), nil
	case strings.Contains(query, "xiangwan_refund_cases"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationRefundDriverValues(state.refundCase)...,
		), nil
	default:
		return nil, errors.New("unexpected Session cancellation SQL")
	}
}

type sessionCancellationSQLStubTransaction struct {
	state *sessionCancellationSQLStubState
}

func (transaction *sessionCancellationSQLStubTransaction) Commit() error {
	transaction.state.commits++
	return nil
}

func (transaction *sessionCancellationSQLStubTransaction) Rollback() error {
	transaction.state.rollbacks++
	return nil
}

type sessionCancellationSQLStubRows struct {
	values []driver.Value
	read   bool
}

func newSessionCancellationSQLStubRows(
	values ...driver.Value,
) *sessionCancellationSQLStubRows {
	return &sessionCancellationSQLStubRows{values: values}
}

func (rows *sessionCancellationSQLStubRows) Columns() []string {
	columns := make([]string, len(rows.values))
	for index := range columns {
		columns[index] = "value"
	}
	return columns
}

func (rows *sessionCancellationSQLStubRows) Close() error {
	return nil
}

func (rows *sessionCancellationSQLStubRows) Next(destinations []driver.Value) error {
	if rows.read {
		return io.EOF
	}
	rows.read = true
	copy(destinations, rows.values)
	return nil
}

func sessionCancellationSessionDriverValues(
	value activity.Session,
) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.InstanceID.String(),
		value.Title,
		string(value.Status),
		sessionCancellationDriverTime(value.RegistrationStartAt),
		sessionCancellationDriverTime(value.RegistrationEndAt),
		sessionCancellationDriverTime(value.SessionStartAt),
		sessionCancellationDriverTime(value.SessionEndAt),
		sessionCancellationDriverInt(value.Capacity),
		sessionCancellationDriverInt(value.GroupMinimum),
		sessionCancellationDriverInt(value.LowStockThreshold),
		sessionCancellationDriverInt64(value.PriceCents),
		sessionCancellationDriverDeliveryMode(value.DeliveryMode),
		sessionCancellationDriverArea(value.Area),
		sessionCancellationDriverString(value.VenueName),
		sessionCancellationDriverString(value.Address),
		sessionCancellationDriverFloat64(value.Longitude),
		sessionCancellationDriverFloat64(value.Latitude),
		sessionCancellationDriverString(value.OnlineParticipationMode),
		sessionCancellationDriverBool(value.OnlineParticipationCompliant),
		int64(value.ConfirmedRegistrationCount),
		int64(value.ActiveHoldCount),
		int64(value.SortOrder),
		sessionCancellationDriverTime(value.PublishedAt),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func sessionCancellationReceiptDriverValues(
	value activity.SessionCancellationReceipt,
) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.SessionID.String(),
		value.PreviewID.String(),
		value.IdempotencyKey,
		value.CancelledBy.String(),
		value.CancellationReason,
		string(value.NotificationStrategy),
		int64(value.CancelledRegistrationCount),
		int64(value.ReleasedConfirmedCount),
		int64(value.ReleasedHoldCount),
		int64(value.ClosedPendingOrderCount),
		int64(value.RefundCaseCount),
		value.RequestedRefundCents,
		int64(value.CouponAdjustmentCount),
		value.CancelledAt,
		value.ResultingSessionVersion,
		value.CreatedAt,
	}
}

func sessionCancellationPreviewDriverValues(
	value activity.SessionCancellationPreview,
) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.SessionID.String(),
		value.RequestedBy.String(),
		value.IdempotencyKey,
		value.SnapshotDigest,
		value.ExpectedSessionVersion,
		int64(value.CancelledRegistrationCount),
		int64(value.ConfirmedRegistrationCount),
		int64(value.ActiveHoldCount),
		int64(value.FreeRegistrationCount),
		int64(value.PaidRefundRegistrationCount),
		int64(value.PendingOrderCount),
		int64(value.UnknownPaymentCount),
		int64(value.RefundCaseCount),
		value.RequestedRefundCents,
		int64(value.CouponAdjustmentCount),
		string(value.NotificationStrategy),
		value.ExpiresAt,
		sessionCancellationDriverTime(value.ConsumedAt),
		value.CreatedAt,
		sessionCancellationDriverUUID(value.ParentInstancePreviewID),
	}
}

func sessionCancellationRegistrationDriverValues(
	value registration.Registration,
) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.SessionID.String(),
		value.PrincipalID.String(),
		string(value.ParticipationStatus),
		value.IdempotencyKey,
		sessionCancellationDriverTime(value.ConfirmedAt),
		sessionCancellationDriverTime(value.CancelledAt),
		sessionCancellationDriverString(value.CancellationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func sessionCancellationOrderDriverValues(value payment.Order) []driver.Value {
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
		sessionCancellationDriverUUIDValue(value.MerchantConfigGenerationID),
		value.OriginalPriceCents,
		value.DiscountCents,
		value.PayableCents,
		sessionCancellationDriverInt64(value.ActualPaidCents),
		sessionCancellationDriverString(value.WeChatTransactionID),
		sessionCancellationDriverTime(value.PaidAt),
		sessionCancellationDriverTime(value.ClosedAt),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func sessionCancellationDriverUUIDValue(value uuid.UUID) driver.Value {
	if value == uuid.Nil {
		return nil
	}
	return value.String()
}

func sessionCancellationHoldDriverValues(
	value payment.CapacityHold,
) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.OrderID.String(),
		value.RegistrationID.String(),
		value.SessionID.String(),
		string(value.HoldStatus),
		value.ExpiresAt,
		sessionCancellationDriverTime(value.ConvertedAt),
		sessionCancellationDriverTime(value.ReleasedAt),
		sessionCancellationDriverString(value.ReleaseReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func sessionCancellationRefundDriverValues(value refund.Case) []driver.Value {
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
		sessionCancellationDriverTime(value.ProcessingStartedAt),
		sessionCancellationDriverTime(value.ResolvedAt),
		sessionCancellationDriverUUID(value.HandledBy),
		sessionCancellationDriverString(value.ExternalRefundID),
		sessionCancellationDriverString(value.EvidenceReference),
		sessionCancellationDriverString(value.OperatorNote),
		sessionCancellationDriverString(value.FailureReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func sessionCancellationDriverTime(value *time.Time) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func sessionCancellationDriverString(value *string) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func sessionCancellationDriverInt(value *int) driver.Value {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func sessionCancellationDriverInt64(value *int64) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func sessionCancellationDriverFloat64(value *float64) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func sessionCancellationDriverBool(value *bool) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func sessionCancellationDriverUUID(value *uuid.UUID) driver.Value {
	if value == nil {
		return nil
	}
	return value.String()
}

func sessionCancellationDriverDeliveryMode(
	value *activity.DeliveryMode,
) driver.Value {
	if value == nil {
		return nil
	}
	return string(*value)
}

func sessionCancellationDriverArea(value *activity.AreaCode) driver.Value {
	if value == nil {
		return nil
	}
	return string(*value)
}

func queriesContain(queries []string, fragment string) bool {
	for _, query := range queries {
		if strings.Contains(query, fragment) {
			return true
		}
	}
	return false
}
