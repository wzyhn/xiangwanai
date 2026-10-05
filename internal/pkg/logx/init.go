// Package logx is the weconq structured-logging entry point.
//
// ADR-007 §Decision (Accepted, zap): production code must use zap structured
// loggers; cmd/api and cmd/worker call MustInit at startup and propagate the
// returned root logger into the gin engine and worker goroutines. Modules read
// the per-request logger via FromContext so each log line is automatically
// bound to the active request_id (see context.go).
//
// This file owns the env → zap.Config mapping. middleware.go owns the gin
// access log; context.go owns the ctx ↔ logger plumbing.
package logx

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// InitLogger returns a root *zap.Logger appropriate for env.
//
//   - env == "production"  → zap.NewProductionConfig with Sampling DISABLED
//     (JSON, ISO8601 ts, info level). Sampling is off because
//     logx.GinAccessLogger emits every request with the constant message
//     "http_access"; under load (>100 req/s/instance) zap's default 100:100
//     sampling would silently drop access lines and their request_ids,
//     breaking the very observability X3.A is meant to provide.
//   - env == "development" → zap.NewDevelopment (console, DEBUG level, caller)
//   - any other value      → falls back to development with a notice field, so
//     misconfigured envs are visible without crashing startup.
//
// Caller is responsible for calling logger.Sync() before exit.
func InitLogger(env string) (*zap.Logger, error) {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "production", "prod":
		cfg := zap.NewProductionConfig()
		cfg.Sampling = nil
		return cfg.Build()
	case "development", "dev", "":
		return zap.NewDevelopment()
	default:
		logger, err := zap.NewDevelopment()
		if err != nil {
			return nil, fmt.Errorf("logx: unrecognized env %q and dev fallback failed: %w", env, err)
		}
		logger.Warn("logx: unrecognized env, falling back to development logger", zap.String("env", env))
		return logger, nil
	}
}

// MustInit is the startup convenience: panics if the underlying zap config
// fails to build. Only call from main(); library code should prefer InitLogger.
func MustInit(env string) *zap.Logger {
	logger, err := InitLogger(env)
	if err != nil {
		panic(fmt.Errorf("logx.MustInit: %w", err))
	}
	return logger
}
