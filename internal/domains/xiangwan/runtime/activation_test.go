package xiangwanruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLoadActivationConfig(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		DatabaseDSNEnv: "postgres://deployment:synthetic@db/xiangwan",
		TenantIDEnv:    "00000000-0000-4000-8000-000000000010",
	}
	config, err := LoadActivationConfig(
		func(name string) (string, bool) {
			value, exists := values[name]
			return value, exists
		},
		"00000000-0000-4000-8000-000000000011",
		7,
		"release 2026-09-13",
	)
	if err != nil {
		t.Fatalf("LoadActivationConfig() error = %v", err)
	}
	if config.ExpectedWriteEpoch != 7 ||
		config.GenerationID.String() !=
			"00000000-0000-4000-8000-000000000011" ||
		config.Reason != "release 2026-09-13" {
		t.Fatalf("LoadActivationConfig() = %+v", config)
	}
}

func TestLoadActivationConfigFailsClosed(t *testing.T) {
	t.Parallel()

	validValues := map[string]string{
		DatabaseDSNEnv: "postgres://deployment:synthetic@db/xiangwan",
		TenantIDEnv:    uuid.NewString(),
	}
	tests := []struct {
		name       string
		values     map[string]string
		generation string
		epoch      int64
		reason     string
	}{
		{name: "missing database", values: map[string]string{}, generation: uuid.NewString(), reason: "release"},
		{name: "invalid generation", values: validValues, generation: "not-a-generation", reason: "release"},
		{name: "negative epoch", values: validValues, generation: uuid.NewString(), epoch: -1, reason: "release"},
		{name: "blank reason", values: validValues, generation: uuid.NewString(), reason: " "},
		{name: "control reason", values: validValues, generation: uuid.NewString(), reason: "release\nnow"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadActivationConfig(
				func(name string) (string, bool) {
					value, exists := test.values[name]
					return value, exists
				},
				test.generation,
				test.epoch,
				test.reason,
			)
			if err == nil {
				t.Fatal("LoadActivationConfig() expected error")
			}
		})
	}
}

func TestActivationValidationDoesNotOpenDatabase(t *testing.T) {
	t.Parallel()

	_, err := (&GenerationActivator{}).Activate(
		context.Background(),
		ActivationCommand{},
	)
	if !errors.Is(err, ErrInvalidGenerationActivation) {
		t.Fatalf("Activate() error = %v", err)
	}

	syntheticDSN := "postgres://deployment:must-not-appear@db/xiangwan"
	_, err = ActivateGeneration(context.Background(), ActivationConfig{
		DatabaseDSN: syntheticDSN,
	})
	if !errors.Is(err, ErrInvalidGenerationActivation) ||
		strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("ActivateGeneration() error = %v", err)
	}
}
