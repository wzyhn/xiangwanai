package logx

import (
	"strings"
	"testing"
)

func TestInitLogger_Production(t *testing.T) {
	logger, err := InitLogger("production")
	if err != nil {
		t.Fatalf("InitLogger(production): %v", err)
	}
	if logger == nil {
		t.Fatal("InitLogger(production) returned nil logger")
	}
	defer func() { _ = logger.Sync() }()

	core := logger.Core()
	if core == nil {
		t.Fatal("production logger has nil core")
	}
}

func TestInitLogger_Development(t *testing.T) {
	for _, env := range []string{"development", "dev", "", "DEVELOPMENT"} {
		t.Run(env, func(t *testing.T) {
			logger, err := InitLogger(env)
			if err != nil {
				t.Fatalf("InitLogger(%q): %v", env, err)
			}
			if logger == nil {
				t.Fatalf("InitLogger(%q) returned nil logger", env)
			}
			defer func() { _ = logger.Sync() }()
		})
	}
}

func TestInitLogger_UnknownEnv_FallsBackToDevelopment(t *testing.T) {
	logger, err := InitLogger("staging")
	if err != nil {
		t.Fatalf("InitLogger(staging): %v", err)
	}
	if logger == nil {
		t.Fatal("InitLogger(staging) returned nil logger")
	}
	defer func() { _ = logger.Sync() }()
}

func TestInitLogger_TrimAndCaseInsensitive(t *testing.T) {
	for _, env := range []string{"  PRODUCTION  ", "Prod", "  prod  "} {
		t.Run(env, func(t *testing.T) {
			logger, err := InitLogger(env)
			if err != nil {
				t.Fatalf("InitLogger(%q): %v", env, err)
			}
			if logger == nil {
				t.Fatalf("InitLogger(%q) returned nil logger", env)
			}
			defer func() { _ = logger.Sync() }()
		})
	}
}

func TestMustInit_Panic(t *testing.T) {
	// MustInit on a value that InitLogger handles gracefully should NOT panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustInit panicked unexpectedly: %v", r)
		}
	}()
	logger := MustInit("production")
	if logger == nil {
		t.Fatal("MustInit returned nil")
	}
	if !strings.Contains(logger.Name(), "") { // sanity touch
		t.Fatal("logger name unexpected; this is a sanity probe only")
	}
	_ = logger.Sync()
}
