package standalonepg

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReadReviewAppliesFixedXiangwanScopeAndCopiesBlocks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	contentID, tenantID, principalID, blockID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var capturedQuery string
	var capturedArgs []any
	reader := &Reader{db: fakeReviewQueryExecutor{query: func(
		_ string,
		query string,
		args ...any,
	) (reviewRows, error) {
		capturedQuery = query
		capturedArgs = append([]any(nil), args...)
		return &fakeReviewRows{rows: [][]any{{
			contentID, tenantID, principalID, "往期回顾", statusActive,
			visibilityPrivate, ownerTypeUser, now, now,
			blockID, "text", int64(0), []byte(`{"text":"hello"}`), now, now,
		}}}, nil
	}}}
	revision := now
	document, err := reader.ReadReview(context.Background(), ReviewReadInput{
		TenantID: tenantID, PrincipalID: principalID, ContentID: contentID,
		RevisionAt: &revision,
	})
	if err != nil {
		t.Fatalf("ReadReview() error = %v", err)
	}
	if document.ID != contentID || document.TenantID != tenantID ||
		document.PrincipalID != principalID || document.UpdatedAt != now ||
		len(document.Blocks) != 1 || document.Blocks[0].ID != blockID ||
		string(document.Blocks[0].Data) != `{"text":"hello"}` {
		t.Fatalf("ReadReview() = %+v", document)
	}
	if !strings.Contains(capturedQuery, "content.metadata->>'product_code' = 'wq-xiangwan'") ||
		!strings.Contains(capturedQuery, "content.updated_at = $4") ||
		!strings.Contains(capturedQuery, "content.visibility = 'private'") {
		t.Fatalf("ReadReview() query omitted fixed scope: %s", capturedQuery)
	}
	if !reflect.DeepEqual(capturedArgs, []any{contentID, tenantID, principalID, now}) {
		t.Fatalf("ReadReview() args = %#v", capturedArgs)
	}
	document.Blocks[0].Data[0] = 'X'
	if string(document.Blocks[0].Data) != `X"text":"hello"}` {
		t.Fatalf("block data did not remain writable copy")
	}
}

func TestReadReviewRejectsMissingOrCorruptRows(t *testing.T) {
	t.Parallel()
	ids := ReviewReadInput{TenantID: uuid.New(), PrincipalID: uuid.New(), ContentID: uuid.New()}
	t.Run("missing", func(t *testing.T) {
		reader := &Reader{db: fakeReviewQueryExecutor{query: func(string, string, ...any) (reviewRows, error) {
			return &fakeReviewRows{}, nil
		}}}
		_, err := reader.ReadReview(context.Background(), ids)
		if !errors.Is(err, ErrReviewNotFound) {
			t.Fatalf("ReadReview() error = %v", err)
		}
	})
	t.Run("partial block", func(t *testing.T) {
		now := time.Now().UTC()
		reader := &Reader{db: fakeReviewQueryExecutor{query: func(string, string, ...any) (reviewRows, error) {
			return &fakeReviewRows{rows: [][]any{{
				ids.ContentID, ids.TenantID, ids.PrincipalID, "draft", statusActive,
				visibilityPrivate, ownerTypeUser, now, now,
				nil, "text", nil, nil, nil, nil,
			}}}, nil
		}}}
		_, err := reader.ReadReview(context.Background(), ids)
		if !errors.Is(err, ErrReviewFactsConflict) {
			t.Fatalf("ReadReview() error = %v", err)
		}
	})
	t.Run("bad block json", func(t *testing.T) {
		now := time.Now().UTC()
		reader := &Reader{db: fakeReviewQueryExecutor{query: func(string, string, ...any) (reviewRows, error) {
			return &fakeReviewRows{rows: [][]any{{
				ids.ContentID, ids.TenantID, ids.PrincipalID, "draft", statusActive,
				visibilityPrivate, ownerTypeUser, now, now,
				uuid.New(), "text", int64(0), []byte("not-json"), now, now,
			}}}, nil
		}}}
		_, err := reader.ReadReview(context.Background(), ids)
		if !errors.Is(err, ErrReviewFactsConflict) {
			t.Fatalf("ReadReview() error = %v", err)
		}
	})
}

func TestStandalonePGValidationAndConstructors(t *testing.T) {
	t.Parallel()
	if _, err := NewWriter(nil); !errors.Is(err, ErrInvalidWriter) {
		t.Fatalf("NewWriter(nil) error = %v", err)
	}
	if _, err := NewReader(nil); !errors.Is(err, ErrInvalidReader) {
		t.Fatalf("NewReader(nil) error = %v", err)
	}
	now := time.Now().UTC()
	base := CreateReviewInput{ID: uuid.New(), TenantID: uuid.New(), PrincipalID: uuid.New(), CreatedAt: now}
	for name, mutate := range map[string]func(*CreateReviewInput){
		"unregistered block": func(in *CreateReviewInput) {
			in.Blocks = []ReviewBlockInput{{ID: uuid.New(), Type: "card", SortOrder: 0, Data: []byte(`{"text":"bad"}`)}}
		},
		"non-object data": func(in *CreateReviewInput) {
			in.Blocks = []ReviewBlockInput{{ID: uuid.New(), Type: "text", SortOrder: 0, Data: []byte(`[]`)}}
		},
		"duplicate order": func(in *CreateReviewInput) {
			in.Blocks = []ReviewBlockInput{
				{ID: uuid.New(), Type: "text", SortOrder: 0, Data: []byte(`{"text":"one"}`)},
				{ID: uuid.New(), Type: "text", SortOrder: 0, Data: []byte(`{"text":"two"}`)},
			}
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if err := validateCreateInput(input); !errors.Is(err, ErrInvalidWriter) {
				t.Fatalf("validateCreateInput() error = %v", err)
			}
		})
	}
}

type fakeReviewQueryExecutor struct {
	query func(string, string, ...any) (reviewRows, error)
}

func (executor fakeReviewQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (reviewRows, error) {
	return executor.query("", query, args...)
}

type fakeReviewRows struct {
	rows  [][]any
	index int
}

func (rows *fakeReviewRows) Next() bool {
	return rows != nil && rows.index < len(rows.rows)
}

func (rows *fakeReviewRows) Scan(destinations ...any) error {
	if rows == nil || rows.index >= len(rows.rows) {
		return sql.ErrNoRows
	}
	values := rows.rows[rows.index]
	rows.index++
	if len(destinations) != len(values) {
		return errors.New("fake row destination mismatch")
	}
	for index, value := range values {
		if err := assignScanValue(destinations[index], value); err != nil {
			return err
		}
	}
	return nil
}

func (rows *fakeReviewRows) Err() error   { return nil }
func (rows *fakeReviewRows) Close() error { return nil }

func assignScanValue(destination any, value any) error {
	switch target := destination.(type) {
	case *uuid.UUID:
		if value == nil {
			return errors.New("nil UUID")
		}
		*target = value.(uuid.UUID)
	case *uuid.NullUUID:
		if value == nil {
			*target = uuid.NullUUID{}
			return nil
		}
		target.UUID, target.Valid = value.(uuid.UUID), true
	case *string:
		*target = value.(string)
	case *sql.NullString:
		if value == nil {
			*target = sql.NullString{}
			return nil
		}
		target.String, target.Valid = value.(string), true
	case *int64:
		*target = value.(int64)
	case *sql.NullInt64:
		if value == nil {
			*target = sql.NullInt64{}
			return nil
		}
		target.Int64, target.Valid = value.(int64), true
	case *time.Time:
		*target = value.(time.Time)
	case *sql.NullTime:
		if value == nil {
			*target = sql.NullTime{}
			return nil
		}
		target.Time, target.Valid = value.(time.Time), true
	case *[]byte:
		if value == nil {
			*target = nil
			return nil
		}
		*target = append([]byte(nil), value.([]byte)...)
	default:
		return errors.New("unsupported fake scan destination")
	}
	return nil
}
