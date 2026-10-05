package activitypostgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestInstanceCancellationSQLAdapterDelegatesAggregatePersistence(t *testing.T) {
	registerInstanceCancellationSQLStub()
	fixture := newInstanceCancellationFixture(t)
	preview, err := activity.NewInstanceCancellationPreview(
		activity.InstanceCancellationPreviewCommand{
			RequestedBy:    fixture.command.ActorID,
			IdempotencyKey: fixture.previewCommand.IdempotencyKey,
			Reason:         fixture.command.Reason,
			At:             fixture.processedAt.Add(-time.Minute),
		},
		fixture.snapshot(t),
	)
	if err != nil {
		t.Fatalf("NewInstanceCancellationPreview() error = %v", err)
	}
	consumedAt := fixture.processedAt
	preview.ConsumedAt = &consumedAt
	cancelled := fixture.instance
	cancelled.Status = activity.InstanceStatusCancelled
	cancelled.Version++
	cancelled.UpdatedAt = fixture.processedAt
	receipt := activity.InstanceCancellationReceipt{
		ID:                           uuid.New(),
		TenantID:                     fixture.target.TenantID,
		SeriesID:                     fixture.target.SeriesID,
		InstanceID:                   fixture.target.InstanceID,
		PreviewID:                    preview.ID,
		IdempotencyKey:               fixture.command.IdempotencyKey,
		CancelledBy:                  fixture.command.ActorID,
		CancellationReason:           fixture.command.Reason,
		NotificationStrategy:         preview.NotificationStrategy,
		SessionCount:                 preview.SessionCount,
		NewlyCancelledSessionCount:   preview.TargetSessionCount,
		AlreadyCancelledSessionCount: preview.AlreadyCancelledSessionCount,
		CancelledRegistrationCount:   preview.CancelledRegistrationCount,
		ReleasedConfirmedCount:       preview.ConfirmedRegistrationCount,
		ReleasedHoldCount:            preview.ActiveHoldCount,
		ClosedPendingOrderCount:      preview.PendingOrderCount,
		RefundCaseCount:              preview.RefundCaseCount,
		RequestedRefundCents:         preview.RequestedRefundCents,
		CouponAdjustmentCount:        preview.CouponAdjustmentCount,
		CancelledAt:                  consumedAt,
		ResultingInstanceVersion:     cancelled.Version,
		CreatedAt:                    consumedAt,
	}
	state := &instanceCancellationSQLStubState{
		target:          fixture.target,
		seriesStatus:    fixture.base.seriesStatus,
		instance:        fixture.instance,
		updatedInstance: cancelled,
		session:         fixture.sessions[0],
		preview:         preview,
		receipt:         receipt,
	}
	instanceCancellationSQLStubCurrent = state
	db, err := sql.Open(instanceCancellationSQLStubDriverName, "adapter")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if value := NewInstanceCancellationPreviewer(db); value.resolver == nil ||
		value.transactions == nil ||
		value.now == nil {
		t.Fatalf("NewInstanceCancellationPreviewer() = %+v", value)
	}
	if value := NewInstanceCanceller(db); value.resolver == nil ||
		value.transactions == nil ||
		value.now == nil {
		t.Fatalf("NewInstanceCanceller() = %+v", value)
	}

	ctx := context.Background()
	target, err := (instanceCancellationSQLResolver{db: db}).
		resolveInstanceCancellationTarget(
			ctx,
			fixture.target.TenantID,
			fixture.target.InstanceID,
		)
	if err != nil || target != fixture.target {
		t.Fatalf("resolveInstanceCancellationTarget() = %+v, %v", target, err)
	}
	tx, err := (instanceCancellationSQLTransactionStarter{db: db}).
		beginInstanceCancellationTx(
			ctx,
			&sql.TxOptions{Isolation: sql.LevelSerializable},
		)
	if err != nil {
		t.Fatalf("beginInstanceCancellationTx() error = %v", err)
	}
	seriesStatus, err := tx.lockSeries(
		ctx,
		fixture.target.TenantID,
		fixture.target.SeriesID,
	)
	if err != nil || seriesStatus != fixture.base.seriesStatus {
		t.Fatalf("lockSeries() = %q, %v", seriesStatus, err)
	}
	instance, err := tx.lockInstanceRecord(
		ctx,
		fixture.target.TenantID,
		fixture.target.SeriesID,
		fixture.target.InstanceID,
	)
	if err != nil || !reflect.DeepEqual(instance, fixture.instance) {
		t.Fatalf("lockInstanceRecord() = %+v, %v", instance, err)
	}
	sessions, err := tx.listInstanceSessions(
		ctx,
		fixture.target.TenantID,
		fixture.target.InstanceID,
	)
	if err != nil || len(sessions) != 1 ||
		!reflect.DeepEqual(sessions[0], fixture.sessions[0]) {
		t.Fatalf("listInstanceSessions() = %+v, %v", sessions, err)
	}
	byKey, err := tx.getInstancePreviewByKey(
		ctx,
		preview.TenantID,
		preview.IdempotencyKey,
	)
	if err != nil || !reflect.DeepEqual(byKey, preview) {
		t.Fatalf("getInstancePreviewByKey() = %+v, %v", byKey, err)
	}
	locked, err := tx.lockInstancePreview(ctx, preview.TenantID, preview.ID)
	if err != nil || !reflect.DeepEqual(locked, preview) {
		t.Fatalf("lockInstancePreview() = %+v, %v", locked, err)
	}
	created, err := tx.createInstancePreview(ctx, preview)
	if err != nil || !reflect.DeepEqual(created, preview) {
		t.Fatalf("createInstancePreview() = %+v, %v", created, err)
	}
	consumed, err := tx.consumeInstancePreview(
		ctx,
		preview.TenantID,
		preview.ID,
		consumedAt,
	)
	if err != nil || !reflect.DeepEqual(consumed, preview) {
		t.Fatalf("consumeInstancePreview() = %+v, %v", consumed, err)
	}
	storedReceipt, err := tx.getInstanceReceipt(
		ctx,
		receipt.TenantID,
		receipt.InstanceID,
	)
	if err != nil || storedReceipt != receipt {
		t.Fatalf("getInstanceReceipt() = %+v, %v", storedReceipt, err)
	}
	updated, err := tx.updateCancelledInstance(
		ctx,
		cancelled,
		fixture.instance.Version,
	)
	if err != nil || !reflect.DeepEqual(updated, cancelled) {
		t.Fatalf("updateCancelledInstance() = %+v, %v", updated, err)
	}
	createdReceipt, err := tx.createInstanceReceipt(ctx, receipt)
	if err != nil || createdReceipt != receipt {
		t.Fatalf("createInstanceReceipt() = %+v, %v", createdReceipt, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	rollbackTx, err := (instanceCancellationSQLTransactionStarter{db: db}).
		beginInstanceCancellationTx(ctx, nil)
	if err != nil {
		t.Fatalf("beginInstanceCancellationTx(rollback) error = %v", err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if state.begins != 2 ||
		state.commits != 1 ||
		state.rollbacks != 1 ||
		state.queries != 11 {
		t.Fatalf("Instance SQL adapter state = %+v", state)
	}
	for _, fragment := range []string{
		"SELECT tenant_id, series_id, id",
		"FROM xiangwan_activity_series",
		"FROM xiangwan_activity_instances",
		"FROM xiangwan_activity_sessions",
		"ORDER BY id",
		"FOR UPDATE",
		"FROM xiangwan_instance_cancellation_previews",
		"INSERT INTO xiangwan_instance_cancellation_previews",
		"UPDATE xiangwan_instance_cancellation_previews",
		"FROM xiangwan_instance_cancellation_receipts",
		"UPDATE xiangwan_activity_instances",
		"INSERT INTO xiangwan_instance_cancellation_receipts",
	} {
		if !queriesContain(state.queryTexts, fragment) {
			t.Fatalf(
				"Instance SQL adapter queries do not contain %q: %v",
				fragment,
				state.queryTexts,
			)
		}
	}
}

const instanceCancellationSQLStubDriverName = "xiangwan-instance-cancellation-postgres-stub"

var (
	registerInstanceCancellationSQLStubOnce sync.Once
	instanceCancellationSQLStubCurrent      *instanceCancellationSQLStubState
)

func registerInstanceCancellationSQLStub() {
	registerInstanceCancellationSQLStubOnce.Do(func() {
		sql.Register(
			instanceCancellationSQLStubDriverName,
			instanceCancellationSQLStubDriver{},
		)
	})
}

type instanceCancellationSQLStubState struct {
	target          instanceCancellationTarget
	seriesStatus    activity.SeriesStatus
	instance        activity.Instance
	updatedInstance activity.Instance
	session         activity.Session
	preview         activity.InstanceCancellationPreview
	receipt         activity.InstanceCancellationReceipt
	queryTexts      []string
	queries         int
	begins          int
	commits         int
	rollbacks       int
}

type instanceCancellationSQLStubDriver struct{}

func (instanceCancellationSQLStubDriver) Open(string) (driver.Conn, error) {
	return &instanceCancellationSQLStubConnection{
		state: instanceCancellationSQLStubCurrent,
	}, nil
}

type instanceCancellationSQLStubConnection struct {
	state *instanceCancellationSQLStubState
}

func (*instanceCancellationSQLStubConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported")
}

func (*instanceCancellationSQLStubConnection) Close() error {
	return nil
}

func (connection *instanceCancellationSQLStubConnection) Begin() (driver.Tx, error) {
	connection.state.begins++
	return &instanceCancellationSQLStubTransaction{state: connection.state}, nil
}

func (connection *instanceCancellationSQLStubConnection) BeginTx(
	context.Context,
	driver.TxOptions,
) (driver.Tx, error) {
	connection.state.begins++
	return &instanceCancellationSQLStubTransaction{state: connection.state}, nil
}

func (connection *instanceCancellationSQLStubConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	state := connection.state
	state.queries++
	state.queryTexts = append(state.queryTexts, query)
	switch {
	case strings.Contains(query, "SELECT tenant_id, series_id, id") &&
		strings.Contains(query, "FROM xiangwan_activity_instances"):
		return newSessionCancellationSQLStubRows(
			state.target.TenantID.String(),
			state.target.SeriesID.String(),
			state.target.InstanceID.String(),
		), nil
	case strings.Contains(query, "FROM xiangwan_activity_series"):
		return newSessionCancellationSQLStubRows(string(state.seriesStatus)), nil
	case strings.Contains(query, "xiangwan_instance_cancellation_previews"):
		return newSessionCancellationSQLStubRows(
			instanceCancellationPreviewDriverValues(state.preview)...,
		), nil
	case strings.Contains(query, "xiangwan_instance_cancellation_receipts"):
		return newSessionCancellationSQLStubRows(
			instanceCancellationReceiptDriverValues(state.receipt)...,
		), nil
	case strings.Contains(query, "UPDATE xiangwan_activity_instances"):
		return newSessionCancellationSQLStubRows(
			instanceCancellationInstanceDriverValues(state.updatedInstance)...,
		), nil
	case strings.Contains(query, "FROM xiangwan_activity_instances"):
		return newSessionCancellationSQLStubRows(
			instanceCancellationInstanceDriverValues(state.instance)...,
		), nil
	case strings.Contains(query, "xiangwan_activity_sessions"):
		return newSessionCancellationSQLStubRows(
			sessionCancellationSessionDriverValues(state.session)...,
		), nil
	default:
		return nil, errors.New("unexpected Instance cancellation SQL")
	}
}

type instanceCancellationSQLStubTransaction struct {
	state *instanceCancellationSQLStubState
}

func (transaction *instanceCancellationSQLStubTransaction) Commit() error {
	transaction.state.commits++
	return nil
}

func (transaction *instanceCancellationSQLStubTransaction) Rollback() error {
	transaction.state.rollbacks++
	return nil
}

func instanceCancellationInstanceDriverValues(
	value activity.Instance,
) []driver.Value {
	quickTagCodes, err := json.Marshal(value.QuickTagCodes)
	if err != nil {
		panic(err)
	}
	detailBlocks, err := json.Marshal(value.DetailBlocks)
	if err != nil {
		panic(err)
	}
	if value.DetailBlocks == nil {
		detailBlocks = []byte("[]")
	}
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.SeriesID.String(),
		value.IssueNo,
		value.Title,
		string(value.Status),
		sessionCancellationDriverString((*string)(value.ActivityType)),
		quickTagCodes,
		value.CoverImageURL,
		detailBlocks,
		value.PublicationVersion,
		value.PresentationRevision,
		sessionCancellationDriverTime(value.ScheduledAt),
		sessionCancellationDriverTime(value.PublishedAt),
		sessionCancellationDriverTime(value.CompletedAt),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func instanceCancellationPreviewDriverValues(
	value activity.InstanceCancellationPreview,
) []driver.Value {
	impacts, err := json.Marshal(value.SessionImpacts)
	if err != nil {
		panic(err)
	}
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.RequestedBy.String(),
		value.IdempotencyKey,
		value.SnapshotDigest,
		value.ExpectedInstanceVersion,
		int64(value.SessionCount),
		int64(value.TargetSessionCount),
		int64(value.AlreadyCancelledSessionCount),
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
		value.CancellationReason,
		string(value.NotificationStrategy),
		impacts,
		value.ExpiresAt,
		sessionCancellationDriverTime(value.ConsumedAt),
		value.CreatedAt,
	}
}

func instanceCancellationReceiptDriverValues(
	value activity.InstanceCancellationReceipt,
) []driver.Value {
	return []driver.Value{
		value.ID.String(),
		value.TenantID.String(),
		value.SeriesID.String(),
		value.InstanceID.String(),
		value.PreviewID.String(),
		value.IdempotencyKey,
		value.CancelledBy.String(),
		value.CancellationReason,
		string(value.NotificationStrategy),
		int64(value.SessionCount),
		int64(value.NewlyCancelledSessionCount),
		int64(value.AlreadyCancelledSessionCount),
		int64(value.CancelledRegistrationCount),
		int64(value.ReleasedConfirmedCount),
		int64(value.ReleasedHoldCount),
		int64(value.ClosedPendingOrderCount),
		int64(value.RefundCaseCount),
		value.RequestedRefundCents,
		int64(value.CouponAdjustmentCount),
		value.CancelledAt,
		value.ResultingInstanceVersion,
		value.CreatedAt,
	}
}
