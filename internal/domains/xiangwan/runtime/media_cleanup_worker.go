package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	storagestand "github.com/wzyhn/xiangwanai/internal/capabilities/storage/standalonepg"
	"github.com/google/uuid"
)

const (
	MediaCleanupWorkerPollInterval = time.Hour
	mediaCleanupBatchLimit         = 100
	mediaCleanupCompletionTimeout  = 5 * time.Second
)

var (
	ErrInvalidMediaCleanupWorker     = errors.New("invalid xiangwan media cleanup worker")
	ErrMediaCleanupSchemaUnavailable = errors.New("xiangwan media cleanup worker schema is unavailable")
)

type mediaCleanupStore interface {
	ClaimNext(context.Context) (storagestand.ReviewCleanupClaim, bool, error)
	MarkExpired(context.Context, storagestand.ReviewCleanupClaim) error
	RecordFailure(context.Context, storagestand.ReviewCleanupClaim, string) error
}

type mediaCleanupObjects interface {
	DeleteSelected(context.Context, uuid.UUID) error
	PruneStaleTemporaries(context.Context, time.Time) (int, error)
}

type mediaCleanupSchemaGate interface {
	CheckMediaCleanupSchema(context.Context) error
}

// MediaCleanupWorker runs only in the isolated Xiangwan customer deployment.
// It never guesses object liveness from filesystem age: the File lifecycle
// claim precedes selected-object deletion and its lease fences completion.
type MediaCleanupWorker struct {
	database       *sql.DB
	store          mediaCleanupStore
	objects        mediaCleanupObjects
	generationGate GenerationGate
	schemaGate     mediaCleanupSchemaGate
	tenantID       uuid.UUID
	generationID   uuid.UUID
	now            func() time.Time
	pollInterval   time.Duration
}

func NewMediaCleanupWorker(
	ctx context.Context, config MediaCleanupWorkerConfig,
) (*MediaCleanupWorker, error) {
	if ctx == nil || !validMediaCleanupWorkerConfig(config) {
		return nil, ErrInvalidMediaCleanupWorker
	}
	database, err := sql.Open("pgx", config.DatabaseDSN)
	if err != nil {
		return nil, ErrDatabaseConfiguration
	}
	database.SetMaxOpenConns(3)
	database.SetMaxIdleConns(1)
	database.SetConnMaxIdleTime(2 * time.Minute)
	database.SetConnMaxLifetime(15 * time.Minute)
	stager, err := storagestand.NewReviewLocalStager(config.StorageDir)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidMediaCleanupWorker
	}
	store, err := storagestand.NewReviewCleanupRepository(
		database, config.TenantID, config.GenerationID,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidMediaCleanupWorker
	}
	executor := sqlGenerationExecutor{db: database}
	return &MediaCleanupWorker{
		database: database, store: store, objects: stager,
		generationGate: &PostgresGenerationGate{db: executor},
		schemaGate:     &postgresMediaCleanupSchemaGate{db: executor},
		tenantID:       config.TenantID, generationID: config.GenerationID,
		now: time.Now, pollInterval: MediaCleanupWorkerPollInterval,
	}, nil
}

func CheckMediaCleanupWorkerReady(
	ctx context.Context, config MediaCleanupWorkerConfig,
) error {
	worker, err := NewMediaCleanupWorker(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = worker.Close() }()
	return worker.Ready(ctx)
}

func (worker *MediaCleanupWorker) Ready(ctx context.Context) error {
	if worker == nil || worker.generationGate == nil || worker.schemaGate == nil ||
		ctx == nil || worker.tenantID == uuid.Nil || worker.generationID == uuid.Nil {
		return ErrInvalidMediaCleanupWorker
	}
	if err := worker.generationGate.CheckActiveGeneration(
		ctx, worker.tenantID, worker.generationID,
	); err != nil {
		return err
	}
	return worker.schemaGate.CheckMediaCleanupSchema(ctx)
}

func (worker *MediaCleanupWorker) Run(ctx context.Context) error {
	if worker == nil || worker.store == nil || worker.objects == nil ||
		worker.now == nil || worker.pollInterval <= 0 || ctx == nil {
		return ErrInvalidMediaCleanupWorker
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := worker.Ready(ctx); err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrGenerationInactive) {
				return nil
			}
			return err
		}
		count, err := worker.processBatch(ctx)
		if err != nil {
			if ctx.Err() != nil ||
				errors.Is(err, storagestand.ErrReviewCleanupGenerationInactive) {
				return nil
			}
			return fmt.Errorf("process xiangwan media cleanup: %w", err)
		}
		if count == mediaCleanupBatchLimit {
			continue
		}
		if _, err := worker.objects.PruneStaleTemporaries(ctx, worker.now()); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("prune xiangwan media temporaries: %w", err)
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

func (worker *MediaCleanupWorker) processBatch(ctx context.Context) (int, error) {
	for count := 0; count < mediaCleanupBatchLimit; count++ {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		claim, found, err := worker.store.ClaimNext(ctx)
		if err != nil || !found {
			return count, err
		}
		deleteErr := worker.objects.DeleteSelected(ctx, claim.FileID)
		// Provider IO has finished. A short detached Phase C reduces the stale
		// lease window during shutdown; an unknown commit safely replays delete.
		completeCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), mediaCleanupCompletionTimeout,
		)
		if deleteErr == nil {
			err = worker.store.MarkExpired(completeCtx, claim)
		} else {
			err = worker.store.RecordFailure(
				completeCtx, claim, "provider_delete_failed",
			)
		}
		cancel()
		if err != nil && !errors.Is(err, storagestand.ErrReviewCleanupLeaseLost) {
			return count + 1, err
		}
	}
	return mediaCleanupBatchLimit, nil
}

func (worker *MediaCleanupWorker) Close() error {
	if worker == nil || worker.database == nil {
		return nil
	}
	return worker.database.Close()
}

type postgresMediaCleanupSchemaGate struct {
	db generationQueryExecutor
}

func (gate *postgresMediaCleanupSchemaGate) CheckMediaCleanupSchema(ctx context.Context) error {
	if gate == nil || gate.db == nil || ctx == nil {
		return ErrInvalidMediaCleanupWorker
	}
	var ready bool
	err := gate.db.queryRowContext(ctx, `
SELECT to_regclass('public.files') IS NOT NULL
   AND to_regclass('public.xiangwan_review_media_uploads') IS NOT NULL
   AND (
       SELECT COUNT(*) FROM information_schema.columns
       WHERE table_schema = 'public' AND table_name = 'files'
         AND column_name IN (
             'delete_after', 'deleting_at', 'expired_at',
             'retry_count', 'last_error', 'file_key'
         )
   ) = 6
`).Scan(&ready)
	if err != nil {
		return fmt.Errorf("check xiangwan media cleanup schema: %w", err)
	}
	if !ready {
		return ErrMediaCleanupSchemaUnavailable
	}
	return nil
}
