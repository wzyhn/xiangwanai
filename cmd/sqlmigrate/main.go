// Standalone distribution migrator. Uses no platform configuration or repair code.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type migration struct {
	name string
	sql  string
	hash string
}
type manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Files         map[string]string `json:"files"`
}

func load(dir string) ([]migration, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var registry manifest
	if err = json.Unmarshal(raw, &registry); err != nil {
		return nil, err
	}
	if registry.SchemaVersion != 1 || len(registry.Files) == 0 {
		return nil, errors.New("invalid migration manifest")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var result []migration
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("non-regular migration %s", name)
		}
		bytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(bytes)
		hash := hex.EncodeToString(sum[:])
		if registry.Files[name] != hash {
			return nil, fmt.Errorf("unregistered or modified migration %s", name)
		}
		if strings.HasSuffix(name, ".up.sql") {
			if strings.Contains(string(bytes), "-- weconq:transaction-break") {
				return nil, fmt.Errorf("unsupported multi-phase migration %s", name)
			}
			result = append(result, migration{name, string(bytes), hash})
		}
	}
	for name := range registry.Files {
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".sql") {
			return nil, errors.New("invalid manifest path")
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return nil, fmt.Errorf("missing registered migration %s", name)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func apply(ctx context.Context, conn *pgx.Conn, files []migration) (int, error) {
	// One session owns the lock through the entire run, including ledger creation.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(1106632772,740)"); err != nil {
		return 0, err
	}
	defer func() { _, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock(1106632772,740)") }()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS xiangwan_distribution_migrations (name TEXT PRIMARY KEY REFERENCES schema_migrations(name), sha256 TEXT NOT NULL);`); err != nil {
		return 0, err
	}
	count := 0
	for _, file := range files {
		var recorded bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)", file.name).Scan(&recorded); err != nil {
			return count, err
		}
		if recorded {
			var hash string
			err := conn.QueryRow(ctx, "SELECT sha256 FROM xiangwan_distribution_migrations WHERE name=$1", file.name).Scan(&hash)
			if errors.Is(err, pgx.ErrNoRows) {
				return count, fmt.Errorf("migration %s has no standalone hash receipt; use a fresh standalone database", file.name)
			}
			if err != nil {
				return count, err
			}
			if hash != file.hash {
				return count, fmt.Errorf("applied migration changed: %s", file.name)
			}
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return count, err
		}
		_, err = tx.Exec(ctx, file.sql)
		if err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO schema_migrations(name) VALUES($1)", file.name)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO xiangwan_distribution_migrations(name,sha256) VALUES($1,$2)", file.name, file.hash)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return count, fmt.Errorf("apply %s: %w", file.name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func run() error {
	dir := flag.String("dir", "deploy/sql", "registered standalone migrations directory")
	flag.Parse()
	files, err := load(*dir)
	if err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		return errors.New("DATABASE_DSN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("database connection failed (credentials omitted)")
	}
	defer conn.Close(ctx)
	count, err := apply(ctx, conn, files)
	if err != nil {
		return err
	}
	fmt.Printf("standalone migrations applied=%d total=%d\n", count, len(files))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
