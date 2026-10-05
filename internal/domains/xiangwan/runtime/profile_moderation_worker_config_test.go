package xiangwanruntime

import (
	"testing"
)

func TestLoadProfileModerationWorkerConfigUsesLeastPrivilegeInputs(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		ProfileModerationDatabaseDSNEnv: "postgres://runtime-placeholder",
		TenantIDEnv:                     runtimeUUID(70).String(),
		GenerationIDEnv:                 runtimeUUID(71).String(),
		AppIDEnv:                        testRuntimeAppID,
		AppSecretEnv:                    "synthetic-app-secret-for-tests",
	}
	config, err := LoadProfileModerationWorkerConfig(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if err != nil || config.DatabaseDSN != values[ProfileModerationDatabaseDSNEnv] ||
		config.TenantID.String() != values[TenantIDEnv] ||
		config.GenerationID.String() != values[GenerationIDEnv] ||
		config.AppID != values[AppIDEnv] ||
		config.AppSecret != values[AppSecretEnv] {
		t.Fatalf("LoadProfileModerationWorkerConfig() = %+v, %v", config, err)
	}
}

func TestLoadProfileModerationWorkerConfigRejectsMissingInputs(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		ProfileModerationDatabaseDSNEnv: "postgres://runtime-placeholder",
		TenantIDEnv:                     runtimeUUID(72).String(),
		GenerationIDEnv:                 runtimeUUID(73).String(),
		AppIDEnv:                        testRuntimeAppID,
		AppSecretEnv:                    "synthetic-app-secret-for-tests",
	}
	for _, missing := range []string{
		ProfileModerationDatabaseDSNEnv,
		TenantIDEnv,
		GenerationIDEnv,
		AppIDEnv,
		AppSecretEnv,
	} {
		missing := missing
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			_, err := LoadProfileModerationWorkerConfig(
				func(name string) (string, bool) {
					if name == missing {
						return "", false
					}
					value, ok := base[name]
					return value, ok
				},
			)
			if err == nil {
				t.Fatalf("missing %s unexpectedly succeeded", missing)
			}
		})
	}
}
