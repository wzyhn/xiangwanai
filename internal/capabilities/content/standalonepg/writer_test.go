package standalonepg

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWriterCreateReviewStampsFixedProductAndPrivateTuple(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	tx := &fakeReviewTransaction{result: fakeResult{rows: 1}}
	writer := &Writer{tx: tx}
	contentID, tenantID, principalID := uuid.New(), uuid.New(), uuid.New()
	blockID := uuid.New()
	document, err := writer.CreateReview(context.Background(), CreateReviewInput{
		ID: contentID, TenantID: tenantID, PrincipalID: principalID,
		Title: "回顾", CreatedAt: now,
		Blocks: []ReviewBlockInput{{
			ID: blockID, Type: "text", SortOrder: 0,
			Data: []byte(`{"text":"hello"}`),
		}},
	})
	if err != nil {
		t.Fatalf("CreateReview() error = %v", err)
	}
	if document.ID != contentID || document.TenantID != tenantID ||
		document.Status != statusActive || document.Visibility != visibilityPrivate ||
		document.OwnerType != ownerTypeUser || len(document.Blocks) != 1 {
		t.Fatalf("CreateReview() = %+v", document)
	}
	if len(tx.execs) != 2 {
		t.Fatalf("CreateReview() exec count = %d, want 2", len(tx.execs))
	}
	if !strings.Contains(tx.execs[0].query, "jsonb_build_object('product_code', 'wq-xiangwan')") ||
		strings.Contains(strings.Join(stringArgs(tx.execs[0].args), ","), "product_code") {
		t.Fatalf("CreateReview() product ownership is caller-controlled or unstamped: %s args=%#v", tx.execs[0].query, tx.execs[0].args)
	}
	if !strings.Contains(tx.execs[0].query, "'review'") ||
		!strings.Contains(tx.execs[0].query, "'private'") ||
		!strings.Contains(tx.execs[0].query, "NULL") {
		t.Fatalf("CreateReview() did not fix review tuple: %s", tx.execs[0].query)
	}
}

func stringArgs(args []any) []string {
	result := make([]string, 0, len(args))
	for _, arg := range args {
		if value, ok := arg.(string); ok {
			result = append(result, value)
		}
	}
	return result
}

type fakeReviewTransaction struct {
	result fakeResult
	execs  []fakeExec
}

func (tx *fakeReviewTransaction) QueryRowContext(
	_ context.Context,
	_ string,
	_ ...any,
) *sql.Row {
	return nil
}

func (tx *fakeReviewTransaction) ExecContext(
	_ context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	tx.execs = append(tx.execs, fakeExec{query: query, args: args})
	return tx.result, nil
}

type fakeExec struct {
	query string
	args  []any
}

type fakeResult struct {
	rows int64
}

func (result fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (result fakeResult) RowsAffected() (int64, error) { return result.rows, nil }
