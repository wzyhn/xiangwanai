package refundpostgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

func TestListQueueUsesTenantStatusAndStableKeysetCursor(t *testing.T) {
	t.Parallel()

	first := pendingRefundCase(t)
	second := first
	second.ID = uuid.New()
	second.OrderID = uuid.New()
	second.RegistrationID = uuid.New()
	second.CreatedAt = first.CreatedAt.Add(time.Second)
	second.UpdatedAt = second.CreatedAt
	lookahead := second
	lookahead.ID = uuid.New()
	lookahead.OrderID = uuid.New()
	lookahead.RegistrationID = uuid.New()
	lookahead.CreatedAt = second.CreatedAt.Add(time.Second)
	lookahead.UpdatedAt = lookahead.CreatedAt

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
					refundQueueItemScanValues(first, "Series A", "Instance A", "Session A"),
					refundQueueItemScanValues(second, "Series B", "Instance B", "Session B"),
					refundQueueItemScanValues(lookahead, "Series C", "Instance C", "Session C"),
				), nil
			}
			return newFakeRows(), nil
		},
	}}

	page, err := repository.ListQueue(context.Background(), refund.QueueFilter{
		TenantID: first.TenantID,
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("ListQueue() error = %v", err)
	}
	if page.ActiveStatus != refund.StatusPendingManual ||
		len(page.Items) != 2 ||
		page.Items[0].Case.ID != first.ID ||
		page.Items[1].SessionTitle != "Session B" ||
		page.NextCursor == "" {
		t.Fatalf("ListQueue() = %+v", page)
	}
	if len(capturedArgs[0]) != 3 ||
		capturedArgs[0][0] != first.TenantID ||
		capturedArgs[0][1] != refund.StatusPendingManual ||
		capturedArgs[0][2] != 3 {
		t.Fatalf("first query args = %#v", capturedArgs[0])
	}
	for _, fragment := range []string{
		"refund_case.tenant_id = $1",
		"refund_case.refund_status = $2",
		"refund_case.refund_status IN ('pending_manual', 'processing', 'failed')",
		"ORDER BY refund_case.created_at ASC, refund_case.id ASC",
		"LIMIT $3",
	} {
		if !strings.Contains(capturedQueries[0], fragment) {
			t.Fatalf("first query %q does not contain %q", capturedQueries[0], fragment)
		}
	}

	next, err := repository.ListQueue(context.Background(), refund.QueueFilter{
		TenantID: first.TenantID,
		Limit:    2,
		Cursor:   page.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListQueue(next) error = %v", err)
	}
	if len(next.Items) != 0 || next.NextCursor != "" {
		t.Fatalf("ListQueue(next) = %+v", next)
	}
	if len(capturedArgs[1]) != 5 ||
		capturedArgs[1][0] != first.TenantID ||
		capturedArgs[1][1] != refund.StatusPendingManual ||
		!capturedArgs[1][2].(time.Time).Equal(second.CreatedAt) ||
		capturedArgs[1][3] != second.ID ||
		capturedArgs[1][4] != 3 {
		t.Fatalf("next query args = %#v", capturedArgs[1])
	}
	if !strings.Contains(
		capturedQueries[1],
		"(refund_case.created_at, refund_case.id) > ($3, $4)",
	) || !strings.Contains(capturedQueries[1], "LIMIT $5") {
		t.Fatalf("next query = %s", capturedQueries[1])
	}
}

func TestListQueueRejectsInvalidOrCrossContextCursors(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	filter := refund.QueueFilter{
		TenantID: tenantID,
		Status:   refund.StatusProcessing,
		Limit:    1,
	}
	cursor, err := encodeRefundQueueCursor(filter, refund.Case{
		ID:        uuid.New(),
		TenantID:  tenantID,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("encodeRefundQueueCursor() error = %v", err)
	}
	unexpectedQuery := func(string, ...any) (rowsScanner, error) {
		t.Fatal("invalid filter unexpectedly reached PostgreSQL")
		return nil, nil
	}
	repository := &Repository{db: &fakeQueryExecutor{queryRows: unexpectedQuery}}
	tests := []struct {
		name    string
		filter  refund.QueueFilter
		wantErr error
	}{
		{
			name:    "tenant required",
			filter:  refund.QueueFilter{},
			wantErr: ErrInvalidRefundQueueFilter,
		},
		{
			name: "terminal status",
			filter: refund.QueueFilter{
				TenantID: tenantID,
				Status:   refund.StatusRefunded,
			},
			wantErr: ErrInvalidRefundQueueFilter,
		},
		{
			name: "limit too large",
			filter: refund.QueueFilter{
				TenantID: tenantID,
				Limit:    refund.MaxQueueLimit + 1,
			},
			wantErr: ErrInvalidRefundQueueFilter,
		},
		{
			name: "malformed cursor",
			filter: refund.QueueFilter{
				TenantID: tenantID,
				Cursor:   "not-base64!",
			},
			wantErr: ErrInvalidRefundQueueCursor,
		},
		{
			name: "cursor tenant changed",
			filter: refund.QueueFilter{
				TenantID: uuid.New(),
				Status:   refund.StatusProcessing,
				Limit:    1,
				Cursor:   cursor,
			},
			wantErr: ErrStaleRefundQueueCursor,
		},
		{
			name: "cursor status changed",
			filter: refund.QueueFilter{
				TenantID: tenantID,
				Status:   refund.StatusFailed,
				Limit:    1,
				Cursor:   cursor,
			},
			wantErr: ErrStaleRefundQueueCursor,
		},
		{
			name: "unknown cursor field",
			filter: refund.QueueFilter{
				TenantID: tenantID,
				Status:   refund.StatusProcessing,
				Limit:    1,
				Cursor: base64.RawURLEncoding.EncodeToString([]byte(
					`{"v":1,"unknown":true}`,
				)),
			},
			wantErr: ErrInvalidRefundQueueCursor,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, gotErr := repository.ListQueue(context.Background(), test.filter)
			if !errors.Is(gotErr, test.wantErr) {
				t.Fatalf("ListQueue() error = %v, want %v", gotErr, test.wantErr)
			}
		})
	}
}

func TestGetCaseDetailReturnsVersionBoundTimeline(t *testing.T) {
	t.Parallel()

	pending := pendingRefundCase(t)
	completed, changed, err := refund.Complete(pending, refund.CompleteCommand{
		HandledBy:         uuid.New(),
		ExternalRefundID:  "wx-refund-detail",
		EvidenceReference: "merchant-console/refunds/detail",
		OperatorNote:      "verified",
		At:                pending.CreatedAt.Add(time.Minute),
	})
	if err != nil || !changed {
		t.Fatalf("Complete() = %+v, %t, %v", completed, changed, err)
	}
	event, err := refund.NewEvent(
		pending,
		completed,
		"refund-operation:detail",
		completed.UpdatedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}

	var detailQuery string
	var detailArgs []any
	var eventQuery string
	var eventArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			detailQuery = query
			detailArgs = append([]any(nil), args...)
			return &fakeRow{values: refundQueueItemScanValues(
				completed,
				"Weekend Walk",
				"September Walk",
				"Morning Session",
			)}
		},
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			eventQuery = query
			eventArgs = append([]any(nil), args...)
			return newFakeRows(refundEventScanValues(event)), nil
		},
	}}

	detail, err := repository.GetCaseDetail(
		context.Background(),
		completed.TenantID,
		completed.ID,
	)
	if err != nil {
		t.Fatalf("GetCaseDetail() error = %v", err)
	}
	if !reflect.DeepEqual(detail.Item.Case, completed) ||
		detail.Item.SeriesTitle != "Weekend Walk" ||
		len(detail.Events) != 1 ||
		!reflect.DeepEqual(detail.Events[0], event) {
		t.Fatalf("GetCaseDetail() = %+v", detail)
	}
	for _, fragment := range []string{
		"refund_case.tenant_id = $1",
		"refund_case.id = $2",
		"activity_instance.series_id = refund_case.series_id",
		"activity_session.instance_id = refund_case.instance_id",
	} {
		if !strings.Contains(detailQuery, fragment) {
			t.Fatalf("detail query %q does not contain %q", detailQuery, fragment)
		}
	}
	if !reflect.DeepEqual(detailArgs, []any{completed.TenantID, completed.ID}) {
		t.Fatalf("detail args = %#v", detailArgs)
	}
	if !strings.Contains(eventQuery, "resulting_refund_version <= $3") ||
		!strings.Contains(eventQuery, "ORDER BY event_sequence ASC") ||
		!reflect.DeepEqual(eventArgs, []any{
			completed.TenantID,
			completed.ID,
			completed.Version,
		}) {
		t.Fatalf("event query/args = %s %#v", eventQuery, eventArgs)
	}
}

func TestQueueRepositoryPreservesReadFailures(t *testing.T) {
	t.Parallel()

	readFailure := errors.New("read failed")
	queueRepository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return nil, readFailure
		},
	}}
	if _, err := queueRepository.ListQueue(context.Background(), refund.QueueFilter{
		TenantID: uuid.New(),
	}); !errors.Is(err, readFailure) {
		t.Fatalf("ListQueue() error = %v", err)
	}

	notFoundRepository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeRow{err: sql.ErrNoRows}
		},
	}}
	if _, err := notFoundRepository.GetCaseDetail(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrRefundCaseNotFound) {
		t.Fatalf("GetCaseDetail(not found) error = %v", err)
	}
	if _, err := notFoundRepository.GetCaseDetail(
		context.Background(),
		uuid.Nil,
		uuid.New(),
	); !errors.Is(err, ErrInvalidRefundQueueFilter) {
		t.Fatalf("GetCaseDetail(invalid) error = %v", err)
	}
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

func refundQueueItemScanValues(
	value refund.Case,
	seriesTitle string,
	instanceTitle string,
	sessionTitle string,
) []any {
	values := refundCaseScanValues(value)
	return append(values, seriesTitle, instanceTitle, sessionTitle)
}
