package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	consumerprofilepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
	wechatpkg "github.com/wzyhn/xiangwanai/internal/pkg/wechat"
	"github.com/google/uuid"
)

const (
	ProfileModerationWorkerPollInterval = time.Second
	profileModerationProviderTimeout    = 15 * time.Second
)

var (
	ErrInvalidProfileModerationWorker = errors.New(
		"invalid xiangwan profile moderation worker",
	)
	ErrProfileModerationWorkerSchemaUnavailable = errors.New(
		"xiangwan profile moderation worker schema is unavailable",
	)
)

type profileModerationStore interface {
	ClaimModerationTask(
		context.Context,
		uuid.UUID,
	) (consumerprofile.ModerationTask, bool, error)
	ProviderOpenID(context.Context, uuid.UUID, string) (string, error)
	ResolveModerationTask(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		consumerprofile.ModerationOutcome,
	) error
}

type profileModerationSchemaGate interface {
	CheckProfileModerationSchema(context.Context) error
}

type ProfileModerationWorker struct {
	database       *sql.DB
	store          profileModerationStore
	checker        contentsecurity.Checker
	generationGate GenerationGate
	schemaGate     profileModerationSchemaGate
	tenantID       uuid.UUID
	generationID   uuid.UUID
	appID          string
	now            func() time.Time
	pollInterval   time.Duration
}

func NewProfileModerationWorker(
	ctx context.Context,
	config ProfileModerationWorkerConfig,
) (*ProfileModerationWorker, error) {
	if ctx == nil || !validProfileModerationWorkerConfig(config) {
		return nil, ErrInvalidProfileModerationWorker
	}
	database, err := openProfileModerationWorkerDatabase(config.DatabaseDSN)
	if err != nil {
		return nil, err
	}
	checker := contentsecurity.NewWechatChecker(
		wechatpkg.NewMiniApp(),
		config.AppID,
		config.AppSecret,
	)
	executor := sqlGenerationExecutor{db: database}
	return &ProfileModerationWorker{
		database: database,
		store: consumerprofilepostgres.NewRepository(
			database,
			config.GenerationID,
		),
		checker:        checker,
		generationGate: &PostgresGenerationGate{db: executor},
		schemaGate:     &postgresProfileModerationSchemaGate{db: executor},
		tenantID:       config.TenantID,
		generationID:   config.GenerationID,
		appID:          config.AppID,
		now:            time.Now,
		pollInterval:   ProfileModerationWorkerPollInterval,
	}, nil
}

func CheckProfileModerationWorkerReady(
	ctx context.Context,
	config ProfileModerationWorkerConfig,
) error {
	if ctx == nil || !validProfileModerationWorkerConfig(config) {
		return ErrInvalidProfileModerationWorker
	}
	database, err := openProfileModerationWorkerDatabase(config.DatabaseDSN)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	executor := sqlGenerationExecutor{db: database}
	worker := &ProfileModerationWorker{
		generationGate: &PostgresGenerationGate{db: executor},
		schemaGate:     &postgresProfileModerationSchemaGate{db: executor},
		tenantID:       config.TenantID,
		generationID:   config.GenerationID,
	}
	return worker.Ready(ctx)
}

func openProfileModerationWorkerDatabase(databaseDSN string) (*sql.DB, error) {
	if databaseDSN == "" {
		return nil, ErrInvalidProfileModerationWorker
	}
	database, err := sql.Open("pgx", databaseDSN)
	if err != nil {
		return nil, ErrDatabaseConfiguration
	}
	database.SetMaxOpenConns(5)
	database.SetMaxIdleConns(2)
	database.SetConnMaxIdleTime(2 * time.Minute)
	database.SetConnMaxLifetime(15 * time.Minute)
	return database, nil
}

func (worker *ProfileModerationWorker) Ready(ctx context.Context) error {
	if worker == nil || worker.generationGate == nil || worker.schemaGate == nil ||
		ctx == nil || worker.tenantID == uuid.Nil || worker.generationID == uuid.Nil {
		return ErrInvalidProfileModerationWorker
	}
	if err := worker.generationGate.CheckActiveGeneration(
		ctx,
		worker.tenantID,
		worker.generationID,
	); err != nil {
		return err
	}
	return worker.schemaGate.CheckProfileModerationSchema(ctx)
}

func (worker *ProfileModerationWorker) Run(ctx context.Context) error {
	if worker == nil || worker.store == nil || worker.checker == nil ||
		worker.now == nil || worker.pollInterval <= 0 || ctx == nil ||
		!validWechatAppID(worker.appID) {
		return ErrInvalidProfileModerationWorker
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := worker.Ready(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrGenerationInactive) {
			return nil
		}
		return err
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		found, err := worker.processNext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, consumerprofilepostgres.ErrGenerationInactive) {
				return nil
			}
			return fmt.Errorf("process xiangwan profile moderation job: %w", err)
		}
		if found {
			continue
		}
		timer := time.NewTimer(worker.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (worker *ProfileModerationWorker) processNext(
	ctx context.Context,
) (bool, error) {
	task, found, err := worker.store.ClaimModerationTask(ctx, worker.tenantID)
	if err != nil || !found {
		return found, err
	}
	if consumerprofile.ValidateModerationTask(task) != nil {
		return true, ErrInvalidProfileModerationWorker
	}
	observedAt := worker.now().UTC().Truncate(time.Microsecond)
	outcome, outcomeErr := consumerprofile.NewUnavailableModerationOutcome(
		observedAt,
	)
	if outcomeErr != nil {
		return true, ErrInvalidProfileModerationWorker
	}
	openID, identityErr := worker.store.ProviderOpenID(
		ctx,
		task.PrincipalID,
		worker.appID,
	)
	if identityErr != nil && !errors.Is(
		identityErr,
		consumerprofilepostgres.ErrProviderIdentityUnavailable,
	) {
		return true, identityErr
	}
	if identityErr == nil {
		providerContext, cancel := context.WithTimeout(
			ctx,
			profileModerationProviderTimeout,
		)
		result, checkErr := worker.checker.CheckText(
			providerContext,
			worker.appID,
			openID,
			consumerprofile.ModerationText(task.Fields),
			1,
		)
		cancel()
		providerObservedAt := worker.now().UTC().Truncate(time.Microsecond)
		if checkErr == nil {
			providerOutcome, providerOutcomeErr :=
				consumerprofile.NewWeChatModerationOutcome(
					string(result.Suggest),
					result.Label,
					result.TraceID,
					providerObservedAt,
				)
			if providerOutcomeErr == nil {
				outcome = providerOutcome
			} else {
				outcome, outcomeErr =
					consumerprofile.NewUnavailableModerationOutcome(
						providerObservedAt,
					)
			}
		} else {
			outcome, outcomeErr =
				consumerprofile.NewUnavailableModerationOutcome(
					providerObservedAt,
				)
		}
		if outcomeErr != nil {
			return true, ErrInvalidProfileModerationWorker
		}
	}
	err = worker.store.ResolveModerationTask(
		ctx,
		worker.tenantID,
		task.CandidateID,
		task.LeaseToken,
		outcome,
	)
	if errors.Is(err, consumerprofilepostgres.ErrModerationLeaseLost) {
		return true, nil
	}
	return true, err
}

func (worker *ProfileModerationWorker) Close() error {
	if worker == nil || worker.database == nil {
		return nil
	}
	return worker.database.Close()
}

type postgresProfileModerationSchemaGate struct {
	db generationQueryExecutor
}

func (gate *postgresProfileModerationSchemaGate) CheckProfileModerationSchema(
	ctx context.Context,
) error {
	if gate == nil || gate.db == nil || ctx == nil {
		return ErrInvalidProfileModerationWorker
	}
	var ready bool
	if err := gate.db.queryRowContext(ctx, `
SELECT
    to_regclass('public.xiangwan_consumer_profile_moderation_jobs') IS NOT NULL
    AND to_regclass(
        'public.xiangwan_consumer_profile_moderation_decisions'
    ) IS NOT NULL
`).Scan(&ready); err != nil {
		return fmt.Errorf("check xiangwan profile moderation schema: %w", err)
	}
	if !ready {
		return ErrProfileModerationWorkerSchemaUnavailable
	}
	return nil
}
