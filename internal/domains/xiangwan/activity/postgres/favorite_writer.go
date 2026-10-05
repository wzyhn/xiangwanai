package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidSeriesFavoriteCommand = errors.New(
		"invalid xiangwan Series favorite command",
	)
	ErrSeriesFavoriteUnavailable = errors.New(
		"xiangwan Series favorite unavailable",
	)
	ErrSeriesFavoritePrincipalUnavailable = errors.New(
		"xiangwan Series favorite principal unavailable",
	)
	ErrSeriesFavoriteTransactionConflict = errors.New(
		"xiangwan Series favorite transaction conflict",
	)
)

type SetSeriesFavoriteCommand struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	SeriesID    uuid.UUID
	Favorited   bool
}

type SeriesFavoriteWriter struct {
	transactions seriesFavoriteTransactionStarter
	now          func() time.Time
}

func NewSeriesFavoriteWriter(db *sql.DB) *SeriesFavoriteWriter {
	return &SeriesFavoriteWriter{
		transactions: sqlSeriesFavoriteTransactionStarter{db: db},
		now:          time.Now,
	}
}

// Set applies a target state under a serializable transaction. Every mutation
// locks the Series row before touching the relation, so favorite_count and the
// unique owner/Series relation commit together without a Redis counter or lock.
func (writer *SeriesFavoriteWriter) Set(
	ctx context.Context,
	command SetSeriesFavoriteCommand,
) (activity.SeriesFavoriteState, error) {
	if writer == nil || writer.transactions == nil || writer.now == nil ||
		ctx == nil || command.TenantID == uuid.Nil ||
		command.PrincipalID == uuid.Nil || command.SeriesID == uuid.Nil {
		return activity.SeriesFavoriteState{}, ErrInvalidSeriesFavoriteCommand
	}
	tx, err := writer.transactions.beginSeriesFavoriteTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return activity.SeriesFavoriteState{}, fmt.Errorf(
			"begin xiangwan Series favorite transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := tx.lockActivePrincipal(ctx, command.PrincipalID); err != nil {
		return activity.SeriesFavoriteState{}, err
	}
	series, err := tx.lockSeries(ctx, command.TenantID, command.SeriesID)
	if err != nil {
		return activity.SeriesFavoriteState{}, err
	}
	if command.Favorited &&
		(series.status != activity.SeriesStatusActive || !series.homeVisible ||
			series.currentPublicInstanceID == nil ||
			!series.currentInstancePublic) {
		return activity.SeriesFavoriteState{}, ErrSeriesFavoriteUnavailable
	}

	occurredAt := writer.now().UTC().Truncate(time.Microsecond)
	if occurredAt.IsZero() {
		return activity.SeriesFavoriteState{},
			ErrSeriesFavoriteTransactionConflict
	}
	changed := false
	if command.Favorited {
		changed, err = tx.insertFavorite(
			ctx,
			uuid.New(),
			command.TenantID,
			command.PrincipalID,
			command.SeriesID,
			occurredAt,
		)
	} else {
		changed, err = tx.deleteFavorite(
			ctx,
			command.TenantID,
			command.PrincipalID,
			command.SeriesID,
		)
	}
	if err != nil {
		return activity.SeriesFavoriteState{}, classifySeriesFavoriteWriteError(err)
	}
	favoriteCount := series.favoriteCount
	seriesVersion := series.version
	if changed {
		delta := int64(1)
		if !command.Favorited {
			delta = -1
		}
		favoriteCount, seriesVersion, err = tx.adjustFavoriteCount(
			ctx,
			command.TenantID,
			command.SeriesID,
			delta,
			series.version,
			occurredAt,
		)
		if err != nil {
			return activity.SeriesFavoriteState{},
				classifySeriesFavoriteWriteError(err)
		}
	}
	result := activity.SeriesFavoriteState{
		TenantID:      command.TenantID,
		PrincipalID:   command.PrincipalID,
		SeriesID:      command.SeriesID,
		Favorited:     command.Favorited,
		Changed:       changed,
		FavoriteCount: favoriteCount,
		SeriesVersion: seriesVersion,
		OccurredAt:    occurredAt,
	}
	if err := activity.ValidateSeriesFavoriteState(result); err != nil {
		return activity.SeriesFavoriteState{},
			ErrSeriesFavoriteTransactionConflict
	}
	if err := tx.Commit(); err != nil {
		return activity.SeriesFavoriteState{}, classifySeriesFavoriteWriteError(err)
	}
	committed = true
	return result, nil
}

type seriesFavoriteFacts struct {
	status                  activity.SeriesStatus
	homeVisible             bool
	currentPublicInstanceID *uuid.UUID
	currentInstancePublic   bool
	favoriteCount           int64
	version                 int64
}

type seriesFavoriteTransactionStarter interface {
	beginSeriesFavoriteTx(
		context.Context,
		*sql.TxOptions,
	) (seriesFavoriteTransaction, error)
}

type seriesFavoriteTransaction interface {
	lockActivePrincipal(context.Context, uuid.UUID) error
	lockSeries(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (seriesFavoriteFacts, error)
	insertFavorite(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (bool, error)
	deleteFavorite(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (bool, error)
	adjustFavoriteCount(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		int64,
		int64,
		time.Time,
	) (int64, int64, error)
	Commit() error
	Rollback() error
}

type sqlSeriesFavoriteTransactionStarter struct {
	db *sql.DB
}

func (starter sqlSeriesFavoriteTransactionStarter) beginSeriesFavoriteTx(
	ctx context.Context,
	options *sql.TxOptions,
) (seriesFavoriteTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidSeriesFavoriteCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlSeriesFavoriteTransaction{tx: tx}, nil
}

type sqlSeriesFavoriteTransaction struct {
	tx *sql.Tx
}

func (tx *sqlSeriesFavoriteTransaction) lockActivePrincipal(
	ctx context.Context,
	principalID uuid.UUID,
) error {
	var lockedID uuid.UUID
	err := tx.tx.QueryRowContext(ctx, `
SELECT id
FROM principals
WHERE id = $1
  AND status = 'active'
  AND deleted_at IS NULL
FOR UPDATE
`, principalID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSeriesFavoritePrincipalUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan Series favorite Principal: %w", err)
	}
	return nil
}

func (tx *sqlSeriesFavoriteTransaction) lockSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (seriesFavoriteFacts, error) {
	var facts seriesFavoriteFacts
	var currentPublicInstanceID uuid.NullUUID
	err := tx.tx.QueryRowContext(ctx, `
SELECT
    activity_series.status,
    activity_series.home_visible,
    activity_series.current_public_instance_id,
    EXISTS (
        SELECT 1
        FROM xiangwan_activity_instances AS activity_instance
        WHERE activity_instance.tenant_id = activity_series.tenant_id
          AND activity_instance.series_id = activity_series.id
          AND activity_instance.id = activity_series.current_public_instance_id
          AND activity_instance.status IN ('published', 'completed', 'cancelled')
    ),
    activity_series.favorite_count,
    activity_series.version
FROM xiangwan_activity_series AS activity_series
WHERE activity_series.tenant_id = $1 AND activity_series.id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(
		&facts.status,
		&facts.homeVisible,
		&currentPublicInstanceID,
		&facts.currentInstancePublic,
		&facts.favoriteCount,
		&facts.version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return seriesFavoriteFacts{}, ErrSeriesFavoriteUnavailable
	}
	if err != nil {
		return seriesFavoriteFacts{}, fmt.Errorf(
			"lock xiangwan favorite Series: %w",
			err,
		)
	}
	if currentPublicInstanceID.Valid {
		value := currentPublicInstanceID.UUID
		facts.currentPublicInstanceID = &value
	}
	if facts.favoriteCount < 0 || facts.version < 1 {
		return seriesFavoriteFacts{}, ErrSeriesFavoriteTransactionConflict
	}
	return facts, nil
}

func (tx *sqlSeriesFavoriteTransaction) insertFavorite(
	ctx context.Context,
	id uuid.UUID,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	seriesID uuid.UUID,
	createdAt time.Time,
) (bool, error) {
	var createdID uuid.UUID
	err := tx.tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_series_favorites (
    id, tenant_id, principal_id, series_id, created_at
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, principal_id, series_id) DO NOTHING
RETURNING id
`, id, tenantID, principalID, seriesID, createdAt).Scan(&createdID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create xiangwan Series favorite: %w", err)
	}
	if createdID != id {
		return false, ErrSeriesFavoriteTransactionConflict
	}
	return true, nil
}

func (tx *sqlSeriesFavoriteTransaction) deleteFavorite(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	seriesID uuid.UUID,
) (bool, error) {
	var deletedID uuid.UUID
	err := tx.tx.QueryRowContext(ctx, `
DELETE FROM xiangwan_series_favorites
WHERE tenant_id = $1 AND principal_id = $2 AND series_id = $3
RETURNING id
`, tenantID, principalID, seriesID).Scan(&deletedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("delete xiangwan Series favorite: %w", err)
	}
	if deletedID == uuid.Nil {
		return false, ErrSeriesFavoriteTransactionConflict
	}
	return true, nil
}

func (tx *sqlSeriesFavoriteTransaction) adjustFavoriteCount(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	delta int64,
	expectedVersion int64,
	occurredAt time.Time,
) (int64, int64, error) {
	if delta != 1 && delta != -1 {
		return 0, 0, ErrSeriesFavoriteTransactionConflict
	}
	var favoriteCount int64
	var seriesVersion int64
	err := tx.tx.QueryRowContext(ctx, `
UPDATE xiangwan_activity_series
SET favorite_count = favorite_count + $3,
    version = version + 1,
    updated_at = $5
WHERE tenant_id = $1
  AND id = $2
  AND favorite_count + $3 >= 0
  AND version = $4
RETURNING favorite_count, version
`, tenantID, seriesID, delta, expectedVersion, occurredAt).Scan(
		&favoriteCount,
		&seriesVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrSeriesFavoriteTransactionConflict
	}
	if err != nil {
		return 0, 0, fmt.Errorf("update xiangwan Series favorite count: %w", err)
	}
	return favoriteCount, seriesVersion, nil
}

func (tx *sqlSeriesFavoriteTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlSeriesFavoriteTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func classifySeriesFavoriteWriteError(err error) error {
	if errors.Is(err, ErrSeriesFavoriteTransactionConflict) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "22003", "23503", "23505", "23514", "40001", "40P01":
			return fmt.Errorf("%w: %v", ErrSeriesFavoriteTransactionConflict, err)
		}
	}
	return err
}
