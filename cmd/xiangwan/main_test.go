package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	xiangwanruntime "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/runtime"
)

func TestExecuteRunsOnlyServeWithValidatedConfig(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		xiangwanruntime.APIDatabaseDSNEnv:           "postgres://runtime:synthetic@db/xiangwan",
		xiangwanruntime.TenantIDEnv:                 "00000000-0000-4000-8000-000000000010",
		xiangwanruntime.GenerationIDEnv:             "00000000-0000-4000-8000-000000000011",
		xiangwanruntime.JWTSigningKeyEnv:            "synthetic-xiangwan-command-signing-key-for-tests",
		xiangwanruntime.AppIDEnv:                    "wx1234567890abcdef",
		xiangwanruntime.AppSecretEnv:                "test" + "-xiangwan-app-secret",
		xiangwanruntime.StorageLocalDirEnv:          "C:\\data\\xiangwan",
		xiangwanruntime.CheckinCredentialHMACKeyEnv: "synthetic-xiangwan-checkin-credential-key-for-tests",
	}
	called := false
	err := execute(
		context.Background(),
		[]string{"serve"},
		func(name string) (string, bool) {
			value, exists := values[name]
			return value, exists
		},
		commandRunners{
			serve: func(_ context.Context, config xiangwanruntime.Config) error {
				called = true
				if config.DatabaseDSN != values[xiangwanruntime.APIDatabaseDSNEnv] ||
					config.TenantID.String() != values[xiangwanruntime.TenantIDEnv] ||
					config.GenerationID.String() !=
						values[xiangwanruntime.GenerationIDEnv] ||
					config.JWTSigningKey != values[xiangwanruntime.JWTSigningKeyEnv] ||
					config.AppID != values[xiangwanruntime.AppIDEnv] ||
					config.AppSecret != values[xiangwanruntime.AppSecretEnv] {
					t.Fatalf("serve config = %+v", config)
				}
				return nil
			},
		},
	)
	if err != nil || !called {
		t.Fatalf("execute(serve) called=%v error=%v", called, err)
	}
}

func TestExecuteRejectsUnknownCommandBeforeReadingEnvironment(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"worker"}, {"serve", "extra"}} {
		args := args
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			t.Parallel()
			if err := execute(
				context.Background(),
				args,
				func(string) (string, bool) {
					t.Fatal("unknown command read environment")
					return "", false
				},
				commandRunners{
					serve: func(context.Context, xiangwanruntime.Config) error {
						t.Fatal("unknown command started server")
						return nil
					},
				},
			); !errors.Is(err, ErrCommandUsage) {
				t.Fatalf("execute(%v) error = %v", args, err)
			}
		})
	}
}

func TestExecuteDoesNotEchoDatabaseCredentialOnConfigFailure(t *testing.T) {
	t.Parallel()

	sensitiveDSN := "postgres://runtime:must-not-appear@db/xiangwan"
	err := execute(
		context.Background(),
		[]string{"serve"},
		func(name string) (string, bool) {
			if name == xiangwanruntime.APIDatabaseDSNEnv {
				return sensitiveDSN, true
			}
			return "", false
		},
		commandRunners{
			serve: func(context.Context, xiangwanruntime.Config) error {
				t.Fatal("invalid config started server")
				return nil
			},
		},
	)
	if err == nil || strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("execute(invalid config) error = %v", err)
	}
}

func TestExecuteActivatesExactExpectedGeneration(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		xiangwanruntime.DatabaseDSNEnv: "postgres://deployment:synthetic@db/xiangwan",
		xiangwanruntime.TenantIDEnv:    "00000000-0000-4000-8000-000000000010",
	}
	called := false
	err := execute(
		context.Background(),
		[]string{
			"activate-generation",
			"--expected-epoch", "4",
			"--generation-id", "00000000-0000-4000-8000-000000000011",
			"--reason", "release 2026-09-13",
		},
		func(name string) (string, bool) {
			value, exists := values[name]
			return value, exists
		},
		commandRunners{
			serve: func(context.Context, xiangwanruntime.Config) error {
				t.Fatal("activation started serve")
				return nil
			},
			activateGeneration: func(
				_ context.Context,
				config xiangwanruntime.ActivationConfig,
			) error {
				called = true
				if config.ExpectedWriteEpoch != 4 ||
					config.GenerationID.String() !=
						"00000000-0000-4000-8000-000000000011" ||
					config.Reason != "release 2026-09-13" {
					t.Fatalf("activation config = %+v", config)
				}
				return nil
			},
		},
	)
	if err != nil || !called {
		t.Fatalf("execute(activate-generation) called=%v error=%v", called, err)
	}
}

func TestExecuteBootstrapsGenerationWithOptionalUUIDs(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		xiangwanruntime.DatabaseDSNEnv: "postgres://deployment:synthetic@db/xiangwan",
	}
	called := false
	err := execute(
		context.Background(),
		[]string{
			"bootstrap-generation",
			"--tenant-id", "00000000-0000-4000-8000-000000000020",
			"--reason", "initial release",
		},
		func(name string) (string, bool) {
			value, exists := values[name]
			return value, exists
		},
		commandRunners{
			bootstrapGeneration: func(
				_ context.Context,
				config xiangwanruntime.BootstrapConfig,
			) error {
				called = true
				if config.DatabaseDSN != "postgres://deployment:synthetic@db/xiangwan" ||
					config.TenantID.String() !=
						"00000000-0000-4000-8000-000000000020" ||
					config.GenerationID != uuid.Nil ||
					config.Reason != "initial release" {
					t.Fatalf("bootstrap config = %+v", config)
				}
				return nil
			},
		},
	)
	if err != nil || !called {
		t.Fatalf("execute(bootstrap-generation) called=%v error=%v", called, err)
	}

	if err := execute(
		context.Background(),
		[]string{"bootstrap-generation", "--reason", "missing dsn"},
		func(string) (string, bool) { return "", false },
		commandRunners{
			bootstrapGeneration: func(context.Context, xiangwanruntime.BootstrapConfig) error {
				t.Fatal("bootstrap ran without DATABASE_DSN")
				return nil
			},
		},
	); err == nil {
		t.Fatal("execute(bootstrap-generation) accepted a missing DATABASE_DSN")
	}
}

func TestExecuteRunsPaymentCloseWorkerWithLeastPrivilegeConfig(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		xiangwanruntime.PaymentCloseDatabaseDSNEnv:                "postgres://worker:synthetic@db/xiangwan",
		xiangwanruntime.TenantIDEnv:                               "00000000-0000-4000-8000-000000000010",
		xiangwanruntime.GenerationIDEnv:                           "00000000-0000-4000-8000-000000000011",
		xiangwanruntime.PaymentCloseAppIDEnv:                      "wx1234567890abcdef",
		xiangwanruntime.PaymentCloseMerchantIDEnv:                 "1900000109",
		xiangwanruntime.PaymentCloseMerchantConfigGenerationIDEnv: "00000000-0000-4000-8000-000000000012",
		xiangwanruntime.PaymentCloseCertSerialEnv:                 "0123456789ABCDEF",
		xiangwanruntime.PaymentClosePrivateKeyFileEnv:             "/run/secrets/merchant-private.pem",
		xiangwanruntime.PaymentClosePublicKeyIDEnv:                "PUB_KEY_ID_0123456789ABCDEF",
		xiangwanruntime.PaymentClosePublicKeyFileEnv:              "/run/secrets/wechat-public.pem",
	}
	called := false
	err := execute(
		context.Background(),
		[]string{"worker"},
		func(name string) (string, bool) {
			value, exists := values[name]
			return value, exists
		},
		commandRunners{worker: func(
			_ context.Context,
			config xiangwanruntime.PaymentCloseWorkerConfig,
		) error {
			called = true
			if config.DatabaseDSN != values[xiangwanruntime.PaymentCloseDatabaseDSNEnv] ||
				config.PaymentAppID != values[xiangwanruntime.PaymentCloseAppIDEnv] ||
				config.PaymentMerchantID !=
					values[xiangwanruntime.PaymentCloseMerchantIDEnv] ||
				config.WeChatPayPrivateKeyFile !=
					values[xiangwanruntime.PaymentClosePrivateKeyFileEnv] {
				t.Fatalf("worker config = %+v", config)
			}
			return nil
		}},
	)
	if err != nil || !called {
		t.Fatalf("execute(worker) called=%t error=%v", called, err)
	}
}

func TestExecuteRunsMediaCleanupWorkerWithOwnCredential(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		xiangwanruntime.MediaCleanupDatabaseDSNEnv: "postgres://cleanup:synthetic@db/xiangwan",
		xiangwanruntime.TenantIDEnv:                uuid.NewString(),
		xiangwanruntime.GenerationIDEnv:            uuid.NewString(),
		xiangwanruntime.StorageLocalDirEnv:         "C:/private/xiangwan",
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	for _, command := range []string{"media-cleanup-worker", "media-cleanup-worker-health"} {
		called := false
		runner := func(_ context.Context, config xiangwanruntime.MediaCleanupWorkerConfig) error {
			called = true
			if config.DatabaseDSN != values[xiangwanruntime.MediaCleanupDatabaseDSNEnv] ||
				config.StorageDir != values[xiangwanruntime.StorageLocalDirEnv] {
				t.Fatalf("%s config = %+v", command, config)
			}
			return nil
		}
		err := execute(context.Background(), []string{command}, lookup,
			commandRunners{
				mediaCleanupWorker: runner, mediaCleanupWorkerHealth: runner,
			})
		if err != nil || !called {
			t.Fatalf("%s called=%t error=%v", command, called, err)
		}
	}
}

func TestExecuteRunsCouponReconciliationWorkerWithOwnCredential(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		xiangwanruntime.CouponReconciliationDatabaseDSNEnv: "postgres://coupon:synthetic@db/xiangwan",
		xiangwanruntime.TenantIDEnv:                        uuid.NewString(),
		xiangwanruntime.GenerationIDEnv:                    uuid.NewString(),
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	for _, command := range []string{"coupon-reconciliation-worker", "coupon-reconciliation-worker-health"} {
		called := false
		runner := func(_ context.Context, config xiangwanruntime.CouponReconciliationWorkerConfig) error {
			called = true
			if config.DatabaseDSN != values[xiangwanruntime.CouponReconciliationDatabaseDSNEnv] {
				t.Fatalf("%s config = %+v", command, config)
			}
			return nil
		}
		err := execute(context.Background(), []string{command}, lookup, commandRunners{
			couponReconciliationWorker: runner, couponReconciliationWorkerHealth: runner,
		})
		if err != nil || !called {
			t.Fatalf("%s called=%t error=%v", command, called, err)
		}
	}
}

func TestExecuteRunsPaymentCloseWorkerHealth(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		xiangwanruntime.PaymentCloseDatabaseDSNEnv:                "postgres://worker:synthetic@db/xiangwan",
		xiangwanruntime.TenantIDEnv:                               "00000000-0000-4000-8000-000000000010",
		xiangwanruntime.GenerationIDEnv:                           "00000000-0000-4000-8000-000000000011",
		xiangwanruntime.PaymentCloseAppIDEnv:                      "wx1234567890abcdef",
		xiangwanruntime.PaymentCloseMerchantIDEnv:                 "1900000109",
		xiangwanruntime.PaymentCloseMerchantConfigGenerationIDEnv: "00000000-0000-4000-8000-000000000012",
		xiangwanruntime.PaymentCloseCertSerialEnv:                 "0123456789ABCDEF",
		xiangwanruntime.PaymentClosePrivateKeyFileEnv:             "/run/secrets/merchant-private.pem",
		xiangwanruntime.PaymentClosePublicKeyIDEnv:                "PUB_KEY_ID_0123456789ABCDEF",
		xiangwanruntime.PaymentClosePublicKeyFileEnv:              "/run/secrets/wechat-public.pem",
	}
	called := false
	err := execute(
		context.Background(),
		[]string{"worker-health"},
		func(name string) (string, bool) {
			value, exists := values[name]
			return value, exists
		},
		commandRunners{workerHealth: func(
			context.Context,
			xiangwanruntime.PaymentCloseWorkerConfig,
		) error {
			called = true
			return nil
		}},
	)
	if err != nil || !called {
		t.Fatalf("execute(worker-health) called=%t error=%v", called, err)
	}
}

func TestExecuteRunsProfileModerationWorkerWithLeastPrivilegeConfig(
	t *testing.T,
) {
	t.Parallel()

	values := map[string]string{
		xiangwanruntime.ProfileModerationDatabaseDSNEnv: "postgres://worker:synthetic@db/xiangwan",
		xiangwanruntime.TenantIDEnv:                     "00000000-0000-4000-8000-000000000010",
		xiangwanruntime.GenerationIDEnv:                 "00000000-0000-4000-8000-000000000011",
		xiangwanruntime.AppIDEnv:                        "wx1234567890abcdef",
		xiangwanruntime.AppSecretEnv:                    "test" + "-xiangwan-app-secret",
	}
	for _, command := range []string{
		"profile-moderation-worker",
		"profile-moderation-worker-health",
	} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			called := false
			runner := func(
				_ context.Context,
				config xiangwanruntime.ProfileModerationWorkerConfig,
			) error {
				called = true
				if config.DatabaseDSN != values[xiangwanruntime.ProfileModerationDatabaseDSNEnv] ||
					config.TenantID.String() != values[xiangwanruntime.TenantIDEnv] ||
					config.GenerationID.String() !=
						values[xiangwanruntime.GenerationIDEnv] ||
					config.AppID != values[xiangwanruntime.AppIDEnv] ||
					config.AppSecret != values[xiangwanruntime.AppSecretEnv] {
					t.Fatalf("profile moderation worker config = %+v", config)
				}
				return nil
			}
			runners := commandRunners{profileWorker: runner}
			if command == "profile-moderation-worker-health" {
				runners = commandRunners{profileWorkerHealth: runner}
			}
			err := execute(
				context.Background(),
				[]string{command},
				func(name string) (string, bool) {
					value, exists := values[name]
					return value, exists
				},
				runners,
			)
			if err != nil || !called {
				t.Fatalf("execute(%s) called=%t error=%v", command, called, err)
			}
		})
	}
}
