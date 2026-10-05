package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const ProductCode = "wq-xiangwan"

var (
	ErrInvalidGenerationGate = errors.New(
		"invalid xiangwan generation gate",
	)
	ErrGenerationInactive = errors.New(
		"xiangwan Runtime generation is not active",
	)
)

type GenerationGate interface {
	CheckActiveGeneration(
		ctx context.Context,
		tenantID uuid.UUID,
		generationID uuid.UUID,
	) error
}

type generationRowScanner interface {
	Scan(destinations ...any) error
}

type generationQueryExecutor interface {
	queryRowContext(
		ctx context.Context,
		query string,
		args ...any,
	) generationRowScanner
}

type sqlGenerationExecutor struct {
	db *sql.DB
}

func (executor sqlGenerationExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) generationRowScanner {
	return executor.db.QueryRowContext(ctx, query, args...)
}

type PostgresGenerationGate struct {
	db generationQueryExecutor
}

func NewPostgresGenerationGate(db *sql.DB) *PostgresGenerationGate {
	if db == nil {
		return &PostgresGenerationGate{}
	}
	return &PostgresGenerationGate{db: sqlGenerationExecutor{db: db}}
}

const activeGenerationSelect = `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_runtime_generations AS runtime_generation
    JOIN tenants AS tenant
      ON tenant.id = runtime_generation.tenant_id
    WHERE runtime_generation.singleton_id = 1
      AND runtime_generation.scope_key = 'wq-xiangwan'
      AND runtime_generation.tenant_id = $1
      AND runtime_generation.active_generation_id = $2
      AND runtime_generation.write_epoch > 0
      AND runtime_generation.bootstrap_completed_at IS NOT NULL
      AND tenant.type = 'business'
      AND tenant.metadata @> '{"product_code":"wq-xiangwan"}'::jsonb
)
`

func (gate *PostgresGenerationGate) CheckActiveGeneration(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	if gate == nil || gate.db == nil || tenantID == uuid.Nil ||
		generationID == uuid.Nil {
		return ErrInvalidGenerationGate
	}
	var active bool
	if err := gate.db.queryRowContext(
		ctx,
		activeGenerationSelect,
		tenantID,
		generationID,
	).Scan(&active); err != nil {
		return fmt.Errorf("check xiangwan active generation: %w", err)
	}
	if !active {
		return ErrGenerationInactive
	}
	return nil
}
