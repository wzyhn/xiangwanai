package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	maxBootstrapReasonRunes   = 500
	bootstrapTenantName       = "xiangwan"
	bootstrapTenantMetadata   = `{"product_code":"wq-xiangwan"}`
	bootstrapSingletonQuery   = `SELECT tenant_id, bootstrap_completed_at, active_generation_id, write_epoch FROM xiangwan_runtime_generations WHERE singleton_id = 1 FOR UPDATE`
	bootstrapTenantInsert     = `INSERT INTO tenants (id, name, type, metadata) VALUES ($1, $2, 'business', $3 ::jsonb)`
	bootstrapGenerationInsert = `INSERT INTO xiangwan_runtime_generations (singleton_id, tenant_id) VALUES (1, $1)`
	bootstrapCompletionUpdate = `UPDATE xiangwan_runtime_generations SET bootstrap_completed_at = clock_timestamp() WHERE singleton_id = 1 AND bootstrap_completed_at IS NULL`
)

var (
	ErrInvalidBootstrapCommand = errors.New("invalid xiangwan bootstrap command")
	ErrBootstrapTenantMismatch = errors.New(
		"xiangwan bootstrap tenant conflicts with the existing authority",
	)
	ErrBootstrapGenerationConflict = errors.New(
		"xiangwan bootstrap cannot replace an active generation; use activate-generation",
	)
	ErrBootstrapUnavailable = errors.New("xiangwan bootstrap is unavailable")
)

// BootstrapConfig drives the one-shot first-boot provisioning command. Both
// UUIDs are optional: omitted IDs are generated and reported back so the
// operator can copy them into the API's runtime environment.
type BootstrapConfig struct {
	DatabaseDSN  string
	TenantID     uuid.UUID
	GenerationID uuid.UUID
	Reason       string
}

// BootstrapCommand is the DSN-free form consumed by the Bootstrapper.
type BootstrapCommand struct {
	TenantID     uuid.UUID
	GenerationID uuid.UUID
	Reason       string
}

type BootstrapResult struct {
	TenantID            uuid.UUID
	GenerationID        uuid.UUID
	WriteEpoch          int64
	AlreadyBootstrapped bool
	Replayed            bool
}

type Bootstrapper struct {
	database *sql.DB
}

func NewBootstrapper(database *sql.DB) *Bootstrapper {
	if database == nil {
		return &Bootstrapper{}
	}
	return &Bootstrapper{database: database}
}

func validBootstrapReason(reason string) bool {
	trimmed := strings.TrimSpace(reason)
	return trimmed != "" && utf8.RuneCountInString(trimmed) <= maxBootstrapReasonRunes &&
		!strings.ContainsAny(trimmed, "\r\n\x00")
}

// BootstrapGeneration provisions the single tenant and the generation
// authority, then activates the first generation in one idempotent command.
// See Bootstrapper.Bootstrap for the exact semantics.
func BootstrapGeneration(
	ctx context.Context,
	config BootstrapConfig,
) (BootstrapResult, error) {
	if ctx == nil || strings.TrimSpace(config.DatabaseDSN) == "" ||
		!validBootstrapReason(config.Reason) {
		return BootstrapResult{}, ErrInvalidBootstrapCommand
	}
	database, err := sql.Open("pgx", config.DatabaseDSN)
	if err != nil {
		return BootstrapResult{}, ErrBootstrapUnavailable
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	return NewBootstrapper(database).Bootstrap(ctx, BootstrapCommand{
		TenantID:     config.TenantID,
		GenerationID: config.GenerationID,
		Reason:       config.Reason,
	})
}

// Bootstrap provisions and activates in one idempotent command:
//
//   - no authority row yet: insert the tenant (metadata.product_code=
//     wq-xiangwan), insert the inert generation singleton, complete
//     bootstrap, and activate at expected epoch 0;
//   - authority present but not activated: complete bootstrap if pending and
//     activate with the recorded (or supplied, matching) generation ID;
//   - already activated: exact replay — the stored tenant/generation pair is
//     reported and the write epoch is not advanced.
//
// The 766/767 guard triggers remain the authority: every write here must be
// one they already accept.
func (bootstrapper *Bootstrapper) Bootstrap(
	ctx context.Context,
	command BootstrapCommand,
) (BootstrapResult, error) {
	if bootstrapper == nil || bootstrapper.database == nil || ctx == nil ||
		!validBootstrapReason(command.Reason) {
		return BootstrapResult{}, ErrInvalidBootstrapCommand
	}
	result, err := bootstrapAuthority(ctx, bootstrapper.database, command)
	if err != nil {
		return BootstrapResult{}, err
	}
	// An active matching pair is a read-only bootstrap replay. It must not
	// repeat epoch-zero activation after cutover or reuse the first reason.
	if result.AlreadyBootstrapped {
		result.Replayed = true
		return result, nil
	}
	activation, err := NewGenerationActivator(bootstrapper.database).Activate(
		ctx,
		ActivationCommand{
			TenantID:           result.TenantID,
			GenerationID:       result.GenerationID,
			ExpectedWriteEpoch: 0,
			Reason:             command.Reason,
		},
	)
	if err != nil {
		return BootstrapResult{}, err
	}
	result.WriteEpoch = activation.WriteEpoch
	result.Replayed = activation.Replayed
	return result, nil
}

func bootstrapAuthority(
	ctx context.Context,
	database *sql.DB,
	config BootstrapCommand,
) (BootstrapResult, error) {
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return BootstrapResult{}, ErrBootstrapUnavailable
	}
	defer func() { _ = transaction.Rollback() }()

	var (
		existingTenant     uuid.NullUUID
		bootstrapCompleted sql.NullTime
		activeGeneration   uuid.NullUUID
		writeEpoch         int64
	)
	switch err := transaction.QueryRowContext(ctx, bootstrapSingletonQuery).Scan(
		&existingTenant,
		&bootstrapCompleted,
		&activeGeneration,
		&writeEpoch,
	); {
	case errors.Is(err, sql.ErrNoRows):
		tenantID := config.TenantID
		if tenantID == uuid.Nil {
			tenantID = uuid.New()
		}
		generationID := config.GenerationID
		if generationID == uuid.Nil {
			generationID = uuid.New()
		}
		if _, err := transaction.ExecContext(
			ctx,
			bootstrapTenantInsert,
			tenantID,
			bootstrapTenantName,
			bootstrapTenantMetadata,
		); err != nil {
			return BootstrapResult{}, ErrBootstrapUnavailable
		}
		if _, err := transaction.ExecContext(
			ctx,
			bootstrapGenerationInsert,
			tenantID,
		); err != nil {
			return BootstrapResult{}, ErrBootstrapUnavailable
		}
		if _, err := transaction.ExecContext(
			ctx,
			bootstrapCompletionUpdate,
		); err != nil {
			return BootstrapResult{}, ErrBootstrapUnavailable
		}
		if err := transaction.Commit(); err != nil {
			return BootstrapResult{}, ErrBootstrapUnavailable
		}
		return BootstrapResult{
			TenantID:     tenantID,
			GenerationID: generationID,
		}, nil
	case err != nil:
		return BootstrapResult{}, ErrBootstrapUnavailable
	}

	if !existingTenant.Valid {
		return BootstrapResult{}, ErrBootstrapUnavailable
	}
	if config.TenantID != uuid.Nil && config.TenantID != existingTenant.UUID {
		return BootstrapResult{}, ErrBootstrapTenantMismatch
	}
	generationID := config.GenerationID
	if activeGeneration.Valid {
		if !bootstrapCompleted.Valid || writeEpoch < 1 {
			return BootstrapResult{}, ErrBootstrapUnavailable
		}
		if generationID != uuid.Nil && generationID != activeGeneration.UUID {
			return BootstrapResult{}, ErrBootstrapGenerationConflict
		}
		generationID = activeGeneration.UUID
	} else if generationID == uuid.Nil {
		generationID = uuid.New()
	}
	if !bootstrapCompleted.Valid {
		if _, err := transaction.ExecContext(
			ctx,
			bootstrapCompletionUpdate,
		); err != nil {
			return BootstrapResult{}, ErrBootstrapUnavailable
		}
	}
	if err := transaction.Commit(); err != nil {
		return BootstrapResult{}, ErrBootstrapUnavailable
	}
	return BootstrapResult{
		TenantID:            existingTenant.UUID,
		GenerationID:        generationID,
		WriteEpoch:          writeEpoch,
		AlreadyBootstrapped: activeGeneration.Valid,
	}, nil
}
