package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const maxActivationReasonRunes = 500

var (
	ErrInvalidGenerationActivation = errors.New(
		"invalid xiangwan generation activation",
	)
	ErrGenerationAuthorityUnavailable = errors.New(
		"xiangwan generation authority is unavailable",
	)
	ErrGenerationActivationConflict = errors.New(
		"xiangwan generation activation conflict",
	)
	ErrBootstrapIncomplete = errors.New(
		"xiangwan bootstrap is incomplete",
	)
	ErrGenerationActivationUnavailable = errors.New(
		"xiangwan generation activation is unavailable",
	)
)

type ActivationConfig struct {
	DatabaseDSN        string
	TenantID           uuid.UUID
	GenerationID       uuid.UUID
	ExpectedWriteEpoch int64
	Reason             string
}

type ActivationCommand struct {
	TenantID           uuid.UUID
	GenerationID       uuid.UUID
	ExpectedWriteEpoch int64
	Reason             string
}

type Activation struct {
	TenantID             uuid.UUID
	GenerationID         uuid.UUID
	PreviousGenerationID uuid.NullUUID
	WriteEpoch           int64
	ActivatedAt          time.Time
	ActivatedBy          string
	Reason               string
	Replayed             bool
}

type activationTransaction interface {
	queryActivationRow(
		ctx context.Context,
		query string,
		args ...any,
	) generationRowScanner
	Commit() error
	Rollback() error
}

type activationDatabase interface {
	Begin(context.Context) (activationTransaction, error)
}

type sqlActivationDatabase struct {
	database *sql.DB
}

func (database sqlActivationDatabase) Begin(
	ctx context.Context,
) (activationTransaction, error) {
	transaction, err := database.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return nil, err
	}
	return sqlActivationTransaction{transaction: transaction}, nil
}

type sqlActivationTransaction struct {
	transaction *sql.Tx
}

func (transaction sqlActivationTransaction) queryActivationRow(
	ctx context.Context,
	query string,
	args ...any,
) generationRowScanner {
	return transaction.transaction.QueryRowContext(ctx, query, args...)
}

func (transaction sqlActivationTransaction) Commit() error {
	return transaction.transaction.Commit()
}

func (transaction sqlActivationTransaction) Rollback() error {
	return transaction.transaction.Rollback()
}

type GenerationActivator struct {
	database activationDatabase
}

func NewGenerationActivator(database *sql.DB) *GenerationActivator {
	if database == nil {
		return &GenerationActivator{}
	}
	return &GenerationActivator{
		database: sqlActivationDatabase{database: database},
	}
}

const generationForActivationSelect = `
SELECT
    runtime_generation.active_generation_id,
    runtime_generation.write_epoch,
    runtime_generation.bootstrap_completed_at,
    runtime_generation.previous_generation_id,
    runtime_generation.activated_at,
    runtime_generation.activated_by,
    runtime_generation.activation_reason,
    current_user
FROM xiangwan_runtime_generations AS runtime_generation
JOIN tenants AS tenant
  ON tenant.id = runtime_generation.tenant_id
WHERE runtime_generation.singleton_id = 1
  AND runtime_generation.scope_key = 'wq-xiangwan'
  AND runtime_generation.tenant_id = $1
  AND tenant.type = 'business'
  AND tenant.metadata @> '{"product_code":"wq-xiangwan"}'::jsonb
FOR UPDATE OF runtime_generation
`

const generationPreviouslyActivatedSelect = `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_runtime_generation_activations
    WHERE scope_key = 'wq-xiangwan'
      AND generation_id = $1
)
`

const activateGenerationUpdate = `
UPDATE xiangwan_runtime_generations
SET previous_generation_id = active_generation_id,
    active_generation_id = $2,
    write_epoch = write_epoch + 1,
    activated_at = clock_timestamp(),
    activated_by = $3,
    activation_reason = $4
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
RETURNING
    tenant_id,
    active_generation_id,
    previous_generation_id,
    write_epoch,
    activated_at,
    activated_by,
    activation_reason
`

func (activator *GenerationActivator) Activate(
	ctx context.Context,
	command ActivationCommand,
) (Activation, error) {
	if activator == nil || activator.database == nil || ctx == nil ||
		!validActivationCommand(command) {
		return Activation{}, ErrInvalidGenerationActivation
	}
	transaction, err := activator.database.Begin(ctx)
	if err != nil {
		return Activation{}, fmt.Errorf("begin generation activation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	var currentGeneration uuid.NullUUID
	var currentWriteEpoch int64
	var bootstrapCompletedAt sql.NullTime
	var previousGeneration uuid.NullUUID
	var activatedAt sql.NullTime
	var activatedBy sql.NullString
	var activationReason sql.NullString
	var databaseActor string
	if err := transaction.queryActivationRow(
		ctx,
		generationForActivationSelect,
		command.TenantID,
	).Scan(
		&currentGeneration,
		&currentWriteEpoch,
		&bootstrapCompletedAt,
		&previousGeneration,
		&activatedAt,
		&activatedBy,
		&activationReason,
		&databaseActor,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Activation{}, ErrGenerationAuthorityUnavailable
		}
		return Activation{}, fmt.Errorf("lock generation authority: %w", err)
	}

	if currentGeneration.Valid && currentGeneration.UUID == command.GenerationID {
		if currentWriteEpoch == command.ExpectedWriteEpoch+1 &&
			activatedBy.Valid && activatedBy.String == databaseActor &&
			activationReason.Valid && activationReason.String == command.Reason &&
			activatedAt.Valid {
			return Activation{
				TenantID:             command.TenantID,
				GenerationID:         command.GenerationID,
				PreviousGenerationID: previousGeneration,
				WriteEpoch:           currentWriteEpoch,
				ActivatedAt:          activatedAt.Time,
				ActivatedBy:          activatedBy.String,
				Reason:               activationReason.String,
				Replayed:             true,
			}, transaction.Commit()
		}
		return Activation{}, ErrGenerationActivationConflict
	}
	if !bootstrapCompletedAt.Valid {
		return Activation{}, ErrBootstrapIncomplete
	}
	if currentWriteEpoch != command.ExpectedWriteEpoch {
		return Activation{}, ErrGenerationActivationConflict
	}

	var previouslyActivated bool
	if err := transaction.queryActivationRow(
		ctx,
		generationPreviouslyActivatedSelect,
		command.GenerationID,
	).Scan(&previouslyActivated); err != nil {
		return Activation{}, fmt.Errorf("check generation history: %w", err)
	}
	if previouslyActivated {
		return Activation{}, ErrGenerationActivationConflict
	}

	activation := Activation{}
	if err := transaction.queryActivationRow(
		ctx,
		activateGenerationUpdate,
		command.TenantID,
		command.GenerationID,
		databaseActor,
		command.Reason,
	).Scan(
		&activation.TenantID,
		&activation.GenerationID,
		&activation.PreviousGenerationID,
		&activation.WriteEpoch,
		&activation.ActivatedAt,
		&activation.ActivatedBy,
		&activation.Reason,
	); err != nil {
		return Activation{}, fmt.Errorf("activate generation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return Activation{}, fmt.Errorf("commit generation activation: %w", err)
	}
	return activation, nil
}

func ActivateGeneration(
	ctx context.Context,
	config ActivationConfig,
) (Activation, error) {
	if ctx == nil || strings.TrimSpace(config.DatabaseDSN) == "" ||
		!validActivationCommand(ActivationCommand{
			TenantID:           config.TenantID,
			GenerationID:       config.GenerationID,
			ExpectedWriteEpoch: config.ExpectedWriteEpoch,
			Reason:             config.Reason,
		}) {
		return Activation{}, ErrInvalidGenerationActivation
	}
	database, err := sql.Open("pgx", config.DatabaseDSN)
	if err != nil {
		return Activation{}, ErrGenerationActivationUnavailable
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	activation, err := NewGenerationActivator(database).Activate(ctx, ActivationCommand{
		TenantID:           config.TenantID,
		GenerationID:       config.GenerationID,
		ExpectedWriteEpoch: config.ExpectedWriteEpoch,
		Reason:             config.Reason,
	})
	if err == nil || errors.Is(err, ErrGenerationAuthorityUnavailable) ||
		errors.Is(err, ErrGenerationActivationConflict) ||
		errors.Is(err, ErrBootstrapIncomplete) ||
		errors.Is(err, ErrInvalidGenerationActivation) {
		return activation, err
	}
	return Activation{}, ErrGenerationActivationUnavailable
}

func LoadActivationConfig(
	lookup EnvironmentLookup,
	generationID string,
	expectedWriteEpoch int64,
	reason string,
) (ActivationConfig, error) {
	if lookup == nil {
		return ActivationConfig{}, ErrInvalidGenerationActivation
	}
	databaseDSN, exists := lookup(DatabaseDSNEnv)
	if !exists || strings.TrimSpace(databaseDSN) == "" {
		return ActivationConfig{}, configError(DatabaseDSNEnv)
	}
	tenantID, err := loadCanonicalUUID(lookup, TenantIDEnv)
	if err != nil {
		return ActivationConfig{}, err
	}
	parsedGenerationID, err := parseCanonicalUUID(generationID)
	if err != nil {
		return ActivationConfig{}, ErrInvalidGenerationActivation
	}
	config := ActivationConfig{
		DatabaseDSN:        strings.TrimSpace(databaseDSN),
		TenantID:           tenantID,
		GenerationID:       parsedGenerationID,
		ExpectedWriteEpoch: expectedWriteEpoch,
		Reason:             reason,
	}
	if !validActivationCommand(ActivationCommand{
		TenantID:           config.TenantID,
		GenerationID:       config.GenerationID,
		ExpectedWriteEpoch: config.ExpectedWriteEpoch,
		Reason:             config.Reason,
	}) {
		return ActivationConfig{}, ErrInvalidGenerationActivation
	}
	return config, nil
}

func validActivationCommand(command ActivationCommand) bool {
	return command.TenantID != uuid.Nil && command.GenerationID != uuid.Nil &&
		command.ExpectedWriteEpoch >= 0 && validActivationReason(command.Reason)
}

func validActivationReason(reason string) bool {
	if reason == "" || strings.TrimSpace(reason) != reason ||
		utf8.RuneCountInString(reason) > maxActivationReasonRunes {
		return false
	}
	for _, character := range reason {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func parseCanonicalUUID(value string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil || parsed.String() != value {
		return uuid.Nil, ErrInvalidGenerationActivation
	}
	return parsed, nil
}
