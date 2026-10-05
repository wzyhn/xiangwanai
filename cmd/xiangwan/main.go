package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"

	xiangwanruntime "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/runtime"
	"github.com/wzyhn/xiangwanai/internal/pkg/logx"
	"go.uber.org/zap"
)

var ErrCommandUsage = errors.New(
	"usage: xiangwan serve | xiangwan worker | xiangwan worker-health | " +
		"xiangwan profile-moderation-worker | " +
		"xiangwan profile-moderation-worker-health | " +
		"xiangwan media-cleanup-worker | " +
		"xiangwan media-cleanup-worker-health | " +
		"xiangwan coupon-reconciliation-worker | " +
		"xiangwan coupon-reconciliation-worker-health | " +
		"xiangwan scheduled-publication-worker | " +
		"xiangwan activate-generation " +
		"--expected-epoch N --generation-id UUID --reason TEXT | " +
		"xiangwan bootstrap-generation " +
		"[--tenant-id UUID] [--generation-id UUID] --reason TEXT | " +
		"xiangwan bootstrap-admin-identity " +
		"--tenant-id UUID --issuer URL --subject TEXT [--nickname TEXT]",
)

type serveCommand func(context.Context, xiangwanruntime.Config) error
type paymentCloseWorkerCommand func(
	context.Context,
	xiangwanruntime.PaymentCloseWorkerConfig,
) error
type profileModerationWorkerCommand func(
	context.Context,
	xiangwanruntime.ProfileModerationWorkerConfig,
) error
type mediaCleanupWorkerCommand func(
	context.Context,
	xiangwanruntime.MediaCleanupWorkerConfig,
) error
type couponReconciliationWorkerCommand func(
	context.Context,
	xiangwanruntime.CouponReconciliationWorkerConfig,
) error
type scheduledPublicationWorkerCommand func(
	context.Context,
	xiangwanruntime.ScheduledPublicationWorkerConfig,
) error
type activateCommand func(
	context.Context,
	xiangwanruntime.ActivationConfig,
) error
type bootstrapCommand func(
	context.Context,
	xiangwanruntime.BootstrapConfig,
) error

type adminSeedCommand func(
	context.Context,
	xiangwanruntime.AdminSeedConfig,
) error

type commandRunners struct {
	serve                            serveCommand
	worker                           paymentCloseWorkerCommand
	workerHealth                     paymentCloseWorkerCommand
	profileWorker                    profileModerationWorkerCommand
	profileWorkerHealth              profileModerationWorkerCommand
	mediaCleanupWorker               mediaCleanupWorkerCommand
	mediaCleanupWorkerHealth         mediaCleanupWorkerCommand
	couponReconciliationWorker       couponReconciliationWorkerCommand
	couponReconciliationWorkerHealth couponReconciliationWorkerCommand
	scheduledPublicationWorker       scheduledPublicationWorkerCommand
	activateGeneration               activateCommand
	bootstrapGeneration              bootstrapCommand
	adminSeed                        adminSeedCommand
}

func main() {
	logger := logx.MustInit(os.Getenv("APP_ENV"))
	defer func() { _ = logger.Sync() }()
	zap.ReplaceGlobals(logger)

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	if err := execute(
		ctx,
		os.Args[1:],
		lookupRuntimeEnvironment,
		commandRunners{
			serve:                            serve,
			worker:                           runPaymentCloseWorker,
			workerHealth:                     checkPaymentCloseWorker,
			profileWorker:                    runProfileModerationWorker,
			profileWorkerHealth:              checkProfileModerationWorker,
			mediaCleanupWorker:               runMediaCleanupWorker,
			mediaCleanupWorkerHealth:         checkMediaCleanupWorker,
			couponReconciliationWorker:       runCouponReconciliationWorker,
			couponReconciliationWorkerHealth: checkCouponReconciliationWorker,
			scheduledPublicationWorker:       runScheduledPublicationWorker,
			activateGeneration:               activateGeneration,
			bootstrapGeneration:              bootstrapGeneration,
			adminSeed:                        bootstrapAdminIdentity,
		},
	); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "xiangwan: %v\n", err)
		os.Exit(1)
	}
}

func lookupRuntimeEnvironment(name string) (string, bool) {
	switch name {
	case xiangwanruntime.APIDatabaseDSNEnv:
		return os.LookupEnv("XIANGWAN_API_DATABASE_DSN")
	case xiangwanruntime.PaymentCloseDatabaseDSNEnv:
		return os.LookupEnv("XIANGWAN_PAYMENT_CLOSE_DATABASE_DSN")
	case xiangwanruntime.ProfileModerationDatabaseDSNEnv:
		return os.LookupEnv("XIANGWAN_PROFILE_MODERATION_DATABASE_DSN")
	case xiangwanruntime.MediaCleanupDatabaseDSNEnv:
		return os.LookupEnv("XIANGWAN_MEDIA_CLEANUP_DATABASE_DSN")
	case xiangwanruntime.CouponReconciliationDatabaseDSNEnv:
		return os.LookupEnv("XIANGWAN_COUPON_RECONCILIATION_DATABASE_DSN")
	case xiangwanruntime.ScheduledPublicationDatabaseDSNEnv:
		return os.LookupEnv("XIANGWAN_SCHEDULED_PUBLICATION_DATABASE_DSN")
	case xiangwanruntime.DatabaseDSNEnv:
		return os.LookupEnv("DATABASE_DSN")
	}
	return os.LookupEnv(name)
}

func execute(
	ctx context.Context,
	args []string,
	lookup xiangwanruntime.EnvironmentLookup,
	runners commandRunners,
) error {
	if len(args) == 0 {
		return ErrCommandUsage
	}
	switch args[0] {
	case "serve":
		if len(args) != 1 || runners.serve == nil {
			return ErrCommandUsage
		}
		config, err := xiangwanruntime.LoadConfig(lookup)
		if err != nil {
			return err
		}
		return runners.serve(ctx, config)
	case "worker", "worker-health":
		if len(args) != 1 {
			return ErrCommandUsage
		}
		runner := runners.worker
		if args[0] == "worker-health" {
			runner = runners.workerHealth
		}
		if runner == nil {
			return ErrCommandUsage
		}
		config, err := xiangwanruntime.LoadPaymentCloseWorkerConfig(lookup)
		if err != nil {
			return err
		}
		return runner(ctx, config)
	case "profile-moderation-worker", "profile-moderation-worker-health":
		if len(args) != 1 {
			return ErrCommandUsage
		}
		runner := runners.profileWorker
		if args[0] == "profile-moderation-worker-health" {
			runner = runners.profileWorkerHealth
		}
		if runner == nil {
			return ErrCommandUsage
		}
		config, err := xiangwanruntime.LoadProfileModerationWorkerConfig(lookup)
		if err != nil {
			return err
		}
		return runner(ctx, config)
	case "media-cleanup-worker", "media-cleanup-worker-health":
		if len(args) != 1 {
			return ErrCommandUsage
		}
		runner := runners.mediaCleanupWorker
		if args[0] == "media-cleanup-worker-health" {
			runner = runners.mediaCleanupWorkerHealth
		}
		if runner == nil {
			return ErrCommandUsage
		}
		config, err := xiangwanruntime.LoadMediaCleanupWorkerConfig(lookup)
		if err != nil {
			return err
		}
		return runner(ctx, config)
	case "coupon-reconciliation-worker", "coupon-reconciliation-worker-health":
		if len(args) != 1 {
			return ErrCommandUsage
		}
		runner := runners.couponReconciliationWorker
		if args[0] == "coupon-reconciliation-worker-health" {
			runner = runners.couponReconciliationWorkerHealth
		}
		if runner == nil {
			return ErrCommandUsage
		}
		config, err := xiangwanruntime.LoadCouponReconciliationWorkerConfig(lookup)
		if err != nil {
			return err
		}
		return runner(ctx, config)
	case "scheduled-publication-worker":
		if len(args) != 1 || runners.scheduledPublicationWorker == nil {
			return ErrCommandUsage
		}
		config, err := xiangwanruntime.LoadScheduledPublicationWorkerConfig(lookup)
		if err != nil {
			return err
		}
		return runners.scheduledPublicationWorker(ctx, config)
	case "activate-generation":
		if runners.activateGeneration == nil {
			return ErrCommandUsage
		}
		config, err := parseActivationConfig(args[1:], lookup)
		if err != nil {
			return err
		}
		return runners.activateGeneration(ctx, config)
	case "bootstrap-generation":
		if runners.bootstrapGeneration == nil {
			return ErrCommandUsage
		}
		config, err := parseBootstrapConfig(args[1:], lookup)
		if err != nil {
			return err
		}
		return runners.bootstrapGeneration(ctx, config)
	case "bootstrap-admin-identity":
		if runners.adminSeed == nil {
			return ErrCommandUsage
		}
		config, err := parseAdminSeedConfig(args[1:], lookup)
		if err != nil {
			return err
		}
		return runners.adminSeed(ctx, config)
	default:
		return ErrCommandUsage
	}
}

func parseActivationConfig(
	args []string,
	lookup xiangwanruntime.EnvironmentLookup,
) (xiangwanruntime.ActivationConfig, error) {
	flags := flag.NewFlagSet("activate-generation", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	expectedWriteEpoch := flags.Int64("expected-epoch", -1, "expected write epoch")
	generationID := flags.String("generation-id", "", "new generation UUID")
	reason := flags.String("reason", "", "deployment reason")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return xiangwanruntime.ActivationConfig{}, ErrCommandUsage
	}
	config, err := xiangwanruntime.LoadActivationConfig(
		lookup,
		*generationID,
		*expectedWriteEpoch,
		*reason,
	)
	if err != nil {
		return xiangwanruntime.ActivationConfig{}, err
	}
	return config, nil
}

func parseAdminSeedConfig(
	args []string,
	lookup xiangwanruntime.EnvironmentLookup,
) (xiangwanruntime.AdminSeedConfig, error) {
	flags := flag.NewFlagSet("bootstrap-admin-identity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tenantID := flags.String("tenant-id", "", "tenant UUID")
	issuer := flags.String("issuer", "", "administrator OIDC issuer URL")
	subject := flags.String("subject", "", "administrator OIDC subject claim")
	nickname := flags.String("nickname", "", "operator display nickname")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return xiangwanruntime.AdminSeedConfig{}, ErrCommandUsage
	}
	databaseDSN, exists := lookup(xiangwanruntime.DatabaseDSNEnv)
	if !exists || strings.TrimSpace(databaseDSN) == "" {
		return xiangwanruntime.AdminSeedConfig{}, ErrCommandUsage
	}
	parsedTenantID, err := uuid.Parse(strings.TrimSpace(*tenantID))
	if err != nil {
		return xiangwanruntime.AdminSeedConfig{}, ErrCommandUsage
	}
	return xiangwanruntime.AdminSeedConfig{
		DatabaseDSN: strings.TrimSpace(databaseDSN),
		TenantID:    parsedTenantID,
		Issuer:      strings.TrimSpace(*issuer),
		Subject:     strings.TrimSpace(*subject),
		Nickname:    strings.TrimSpace(*nickname),
	}, nil
}

func parseBootstrapConfig(
	args []string,
	lookup xiangwanruntime.EnvironmentLookup,
) (xiangwanruntime.BootstrapConfig, error) {
	flags := flag.NewFlagSet("bootstrap-generation", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tenantID := flags.String("tenant-id", "", "tenant UUID override")
	generationID := flags.String("generation-id", "", "generation UUID override")
	reason := flags.String("reason", "", "deployment reason")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return xiangwanruntime.BootstrapConfig{}, ErrCommandUsage
	}
	databaseDSN, exists := lookup(xiangwanruntime.DatabaseDSNEnv)
	if !exists || strings.TrimSpace(databaseDSN) == "" {
		return xiangwanruntime.BootstrapConfig{}, ErrCommandUsage
	}
	config := xiangwanruntime.BootstrapConfig{
		DatabaseDSN: strings.TrimSpace(databaseDSN),
		Reason:      strings.TrimSpace(*reason),
	}
	if strings.TrimSpace(*tenantID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*tenantID))
		if err != nil {
			return xiangwanruntime.BootstrapConfig{}, ErrCommandUsage
		}
		config.TenantID = parsed
	}
	if strings.TrimSpace(*generationID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*generationID))
		if err != nil {
			return xiangwanruntime.BootstrapConfig{}, ErrCommandUsage
		}
		config.GenerationID = parsed
	}
	return config, nil
}

func serve(ctx context.Context, config xiangwanruntime.Config) error {
	// HTTP handlers derive work from each request context; the process context
	// below owns only the server lifetime.
	//nolint:contextcheck
	server, err := xiangwanruntime.NewServer(config)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()
	return server.Run(ctx)
}

func runPaymentCloseWorker(
	ctx context.Context,
	config xiangwanruntime.PaymentCloseWorkerConfig,
) error {
	worker, err := xiangwanruntime.NewPaymentCloseWorker(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = worker.Close() }()
	return worker.Run(ctx)
}

func checkPaymentCloseWorker(
	ctx context.Context,
	config xiangwanruntime.PaymentCloseWorkerConfig,
) error {
	return xiangwanruntime.CheckPaymentCloseWorkerReady(ctx, config)
}

func runProfileModerationWorker(
	ctx context.Context,
	config xiangwanruntime.ProfileModerationWorkerConfig,
) error {
	worker, err := xiangwanruntime.NewProfileModerationWorker(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = worker.Close() }()
	return worker.Run(ctx)
}

func checkProfileModerationWorker(
	ctx context.Context,
	config xiangwanruntime.ProfileModerationWorkerConfig,
) error {
	return xiangwanruntime.CheckProfileModerationWorkerReady(ctx, config)
}

func runMediaCleanupWorker(
	ctx context.Context,
	config xiangwanruntime.MediaCleanupWorkerConfig,
) error {
	worker, err := xiangwanruntime.NewMediaCleanupWorker(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = worker.Close() }()
	return worker.Run(ctx)
}

func checkMediaCleanupWorker(
	ctx context.Context,
	config xiangwanruntime.MediaCleanupWorkerConfig,
) error {
	return xiangwanruntime.CheckMediaCleanupWorkerReady(ctx, config)
}

func runCouponReconciliationWorker(
	ctx context.Context,
	config xiangwanruntime.CouponReconciliationWorkerConfig,
) error {
	worker, err := xiangwanruntime.NewCouponReconciliationWorker(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = worker.Close() }()
	return worker.Run(ctx)
}

func checkCouponReconciliationWorker(
	ctx context.Context,
	config xiangwanruntime.CouponReconciliationWorkerConfig,
) error {
	return xiangwanruntime.CheckCouponReconciliationWorkerReady(ctx, config)
}

func runScheduledPublicationWorker(
	ctx context.Context,
	config xiangwanruntime.ScheduledPublicationWorkerConfig,
) error {
	worker, err := xiangwanruntime.NewScheduledPublicationProcess(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = worker.Close() }()
	return worker.Run(ctx)
}

func activateGeneration(
	ctx context.Context,
	config xiangwanruntime.ActivationConfig,
) error {
	activation, err := xiangwanruntime.ActivateGeneration(ctx, config)
	if err != nil {
		return err
	}
	status := "activated"
	if activation.Replayed {
		status = "already_active"
	}
	_, _ = fmt.Fprintf(
		os.Stdout,
		"xiangwan generation %s: write_epoch=%d generation_id=%s\n",
		status,
		activation.WriteEpoch,
		activation.GenerationID,
	)
	return nil
}

func bootstrapGeneration(
	ctx context.Context,
	config xiangwanruntime.BootstrapConfig,
) error {
	result, err := xiangwanruntime.BootstrapGeneration(ctx, config)
	if err != nil {
		return err
	}
	status := "bootstrapped"
	if result.Replayed {
		status = "already_active"
	}
	_, _ = fmt.Fprintf(
		os.Stdout,
		"xiangwan generation %s: tenant_id=%s generation_id=%s write_epoch=%d\n",
		status,
		result.TenantID,
		result.GenerationID,
		result.WriteEpoch,
	)
	if !result.AlreadyBootstrapped && !result.Replayed {
		_, _ = fmt.Fprint(
			os.Stdout,
			"copy tenant_id and generation_id into the API environment "+
				"(XIANGWAN_TENANT_ID / XIANGWAN_GENERATION_ID)\n",
		)
	}
	return nil
}

func bootstrapAdminIdentity(
	ctx context.Context,
	config xiangwanruntime.AdminSeedConfig,
) error {
	result, err := xiangwanruntime.BootstrapAdminIdentity(ctx, config)
	if err != nil {
		return err
	}
	status := "seeded"
	if result.AlreadySeeded {
		status = "already_linked"
	}
	_, _ = fmt.Fprintf(
		os.Stdout,
		"xiangwan administrator %s: tenant_id=%s principal_id=%s identity_link_id=%s grant_id=%s\n",
		status,
		result.TenantID,
		result.PrincipalID,
		result.IdentityLinkID,
		result.GrantID,
	)
	if !result.AlreadySeeded {
		_, _ = fmt.Fprint(
			os.Stdout,
			"the linked OIDC identity can now complete the first administrator login\n",
		)
	}
	return nil
}
