package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSeriesFavoriteWriterCreatesAndCountsInOneTransaction(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	tx := &fakeSeriesFavoriteTransaction{
		series: seriesFavoriteFacts{
			status:                  activity.SeriesStatusActive,
			homeVisible:             true,
			currentPublicInstanceID: &instanceID,
			currentInstancePublic:   true,
			favoriteCount:           3,
			version:                 7,
		},
		inserted:              true,
		adjustedCount:         4,
		adjustedSeriesVersion: 8,
	}
	starter := &fakeSeriesFavoriteTransactionStarter{tx: tx}
	writer := &SeriesFavoriteWriter{transactions: starter, now: func() time.Time { return now }}
	state, err := writer.Set(context.Background(), SetSeriesFavoriteCommand{
		TenantID:    tenantID,
		PrincipalID: principalID,
		SeriesID:    seriesID,
		Favorited:   true,
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if !state.Favorited || !state.Changed || state.FavoriteCount != 4 ||
		state.SeriesVersion != 8 || !state.OccurredAt.Equal(now) ||
		starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable ||
		tx.principalID != principalID || tx.tenantID != tenantID ||
		tx.seriesID != seriesID || tx.insertID == uuid.Nil ||
		tx.insertPrincipalID != principalID || tx.delta != 1 ||
		tx.expectedVersion != 7 || !tx.adjustedAt.Equal(now) ||
		tx.commitCalls != 1 || tx.rollbackCalls != 0 {
		t.Fatalf("state=%+v starter=%+v tx=%+v", state, starter, tx)
	}
}

func TestSeriesFavoriteWriterTargetStateRetriesDoNotMoveCounter(t *testing.T) {
	t.Parallel()

	instanceID := uuid.New()
	tests := []struct {
		name       string
		favorited  bool
		count      int64
		version    int64
		inserted   bool
		deleted    bool
		wantChange bool
	}{
		{
			name:      "already favorited",
			favorited: true,
			count:     9,
			version:   11,
		},
		{
			name:      "already absent",
			favorited: false,
			count:     2,
			version:   5,
		},
		{
			name:       "remove existing",
			favorited:  false,
			count:      3,
			version:    6,
			deleted:    true,
			wantChange: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tx := &fakeSeriesFavoriteTransaction{
				series: seriesFavoriteFacts{
					status:                  activity.SeriesStatusActive,
					homeVisible:             true,
					currentPublicInstanceID: &instanceID,
					currentInstancePublic:   true,
					favoriteCount:           test.count,
					version:                 test.version,
				},
				inserted: test.inserted,
				deleted:  test.deleted,
			}
			if test.wantChange {
				tx.adjustedCount = test.count - 1
				tx.adjustedSeriesVersion = test.version + 1
			}
			writer := &SeriesFavoriteWriter{
				transactions: &fakeSeriesFavoriteTransactionStarter{tx: tx},
				now:          time.Now,
			}
			state, err := writer.Set(context.Background(), SetSeriesFavoriteCommand{
				TenantID:    uuid.New(),
				PrincipalID: uuid.New(),
				SeriesID:    uuid.New(),
				Favorited:   test.favorited,
			})
			if err != nil {
				t.Fatalf("Set() error = %v", err)
			}
			wantCount := test.count
			wantVersion := test.version
			if test.wantChange {
				wantCount--
				wantVersion++
			}
			if state.Favorited != test.favorited ||
				state.Changed != test.wantChange ||
				state.FavoriteCount != wantCount ||
				state.SeriesVersion != wantVersion ||
				tx.adjustCalls != boolToInt(test.wantChange) ||
				tx.commitCalls != 1 {
				t.Fatalf("state=%+v tx=%+v", state, tx)
			}
		})
	}
}

func TestSeriesFavoriteWriterRejectsUnavailableTargetAndRollsBack(t *testing.T) {
	t.Parallel()

	tx := &fakeSeriesFavoriteTransaction{series: seriesFavoriteFacts{
		status:        activity.SeriesStatusDraft,
		homeVisible:   true,
		favoriteCount: 1,
		version:       2,
	}}
	writer := &SeriesFavoriteWriter{
		transactions: &fakeSeriesFavoriteTransactionStarter{tx: tx},
		now:          time.Now,
	}
	_, err := writer.Set(context.Background(), SetSeriesFavoriteCommand{
		TenantID:    uuid.New(),
		PrincipalID: uuid.New(),
		SeriesID:    uuid.New(),
		Favorited:   true,
	})
	if !errors.Is(err, ErrSeriesFavoriteUnavailable) || tx.insertCalls != 0 ||
		tx.adjustCalls != 0 || tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("error=%v tx=%+v", err, tx)
	}
}

func TestSeriesFavoriteWriterValidatesAndClassifiesConflicts(t *testing.T) {
	t.Parallel()

	writer := &SeriesFavoriteWriter{
		transactions: &fakeSeriesFavoriteTransactionStarter{},
		now:          time.Now,
	}
	if _, err := writer.Set(context.Background(), SetSeriesFavoriteCommand{}); !errors.Is(
		err,
		ErrInvalidSeriesFavoriteCommand,
	) {
		t.Fatalf("invalid command error = %v", err)
	}
	classified := classifySeriesFavoriteWriteError(&pgconn.PgError{Code: "40001"})
	if !errors.Is(classified, ErrSeriesFavoriteTransactionConflict) {
		t.Fatalf("classified error = %v", classified)
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type fakeSeriesFavoriteTransactionStarter struct {
	tx      seriesFavoriteTransaction
	err     error
	options *sql.TxOptions
}

func (starter *fakeSeriesFavoriteTransactionStarter) beginSeriesFavoriteTx(
	_ context.Context,
	options *sql.TxOptions,
) (seriesFavoriteTransaction, error) {
	starter.options = options
	return starter.tx, starter.err
}

type fakeSeriesFavoriteTransaction struct {
	series                seriesFavoriteFacts
	principalErr          error
	seriesErr             error
	inserted              bool
	insertErr             error
	deleted               bool
	deleteErr             error
	adjustedCount         int64
	adjustedSeriesVersion int64
	adjustErr             error
	commitErr             error

	principalID       uuid.UUID
	tenantID          uuid.UUID
	seriesID          uuid.UUID
	insertID          uuid.UUID
	insertPrincipalID uuid.UUID
	delta             int64
	expectedVersion   int64
	adjustedAt        time.Time
	insertCalls       int
	deleteCalls       int
	adjustCalls       int
	commitCalls       int
	rollbackCalls     int
}

func (tx *fakeSeriesFavoriteTransaction) lockActivePrincipal(
	_ context.Context,
	principalID uuid.UUID,
) error {
	tx.principalID = principalID
	return tx.principalErr
}

func (tx *fakeSeriesFavoriteTransaction) lockSeries(
	_ context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (seriesFavoriteFacts, error) {
	tx.tenantID = tenantID
	tx.seriesID = seriesID
	return tx.series, tx.seriesErr
}

func (tx *fakeSeriesFavoriteTransaction) insertFavorite(
	_ context.Context,
	id uuid.UUID,
	_ uuid.UUID,
	principalID uuid.UUID,
	_ uuid.UUID,
	_ time.Time,
) (bool, error) {
	tx.insertCalls++
	tx.insertID = id
	tx.insertPrincipalID = principalID
	return tx.inserted, tx.insertErr
}

func (tx *fakeSeriesFavoriteTransaction) deleteFavorite(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	_ uuid.UUID,
) (bool, error) {
	tx.deleteCalls++
	return tx.deleted, tx.deleteErr
}

func (tx *fakeSeriesFavoriteTransaction) adjustFavoriteCount(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	delta int64,
	expectedVersion int64,
	at time.Time,
) (int64, int64, error) {
	tx.adjustCalls++
	tx.delta = delta
	tx.expectedVersion = expectedVersion
	tx.adjustedAt = at
	return tx.adjustedCount, tx.adjustedSeriesVersion, tx.adjustErr
}

func (tx *fakeSeriesFavoriteTransaction) Commit() error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *fakeSeriesFavoriteTransaction) Rollback() error {
	tx.rollbackCalls++
	return nil
}
