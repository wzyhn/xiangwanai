package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	adminpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin/postgres"
	"github.com/google/uuid"
)

var ErrInvalidScheduledPublicationWorker = errors.New("invalid xiangwan scheduled publication worker")

type ScheduledPublicationProcess struct {
	database *sql.DB
	catalog  *adminpostgres.Catalog
	worker   ScheduledPublicationWorker
	poll     time.Duration
}

func NewScheduledPublicationProcess(
	ctx context.Context,
	config ScheduledPublicationWorkerConfig,
) (*ScheduledPublicationProcess, error) {
	if ctx == nil || strings.TrimSpace(config.DatabaseDSN) == "" ||
		config.TenantID == uuid.Nil || config.GenerationID == uuid.Nil {
		return nil, ErrInvalidScheduledPublicationWorker
	}
	database, err := sql.Open("pgx", config.DatabaseDSN)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(2)
	authorizer, err := adminpostgres.NewGrantAuthorizer(config.TenantID)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	catalog, err := adminpostgres.NewCatalog(database, config.TenantID, config.GenerationID, authorizer)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	poll := config.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	gate := NewPostgresGenerationGate(database)
	return &ScheduledPublicationProcess{
		database: database, catalog: catalog,
		worker: ScheduledPublicationWorker{
			DB: database, TenantID: config.TenantID,
			GenerationID: config.GenerationID,
			GenerationOK: func(ctx context.Context) error {
				return gate.CheckActiveGeneration(ctx, config.TenantID, config.GenerationID)
			},
		},
		poll: poll,
	}, nil
}

func (process *ScheduledPublicationProcess) Close() error {
	if process == nil || process.database == nil {
		return nil
	}
	return process.database.Close()
}

func (process *ScheduledPublicationProcess) Run(ctx context.Context) error {
	if process == nil || process.catalog == nil || process.poll <= 0 || ctx == nil {
		return ErrInvalidScheduledPublicationWorker
	}
	for ctx.Err() == nil {
		_, err := process.worker.RunOnce(ctx, func(ctx context.Context, task ScheduledPublicationTask) error {
			return PublishScheduledPublication(ctx, task, func(ctx context.Context, command xiangwanadmin.PublishInstanceCommand) error {
				_, err := process.catalog.PublishInstance(ctx, command)
				return err
			})
		})
		if err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-time.After(process.poll):
			}
			continue
		}
		if err == nil {
			select {
			case <-ctx.Done():
			case <-time.After(process.poll):
			}
		}
	}
	return nil
}
