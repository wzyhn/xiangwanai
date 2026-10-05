package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestRejectsMutationAndUnregisteredFile(t *testing.T) {
	dir := t.TempDir()
	sql := []byte("CREATE TABLE example(id INTEGER);")
	name := "001_example.up.sql"
	_ = os.WriteFile(filepath.Join(dir, name), sql, 0600)
	sum := sha256.Sum256(sql)
	raw, _ := json.Marshal(manifest{1, map[string]string{name: hex.EncodeToString(sum[:])}})
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600)
	if _, err := load(dir); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, name), []byte("SELECT 1;"), 0600)
	if _, err := load(dir); err == nil {
		t.Fatal("mutation accepted")
	}
	_ = os.WriteFile(filepath.Join(dir, name), sql, 0600)
	_ = os.WriteFile(filepath.Join(dir, "002_unregistered.up.sql"), sql, 0600)
	if _, err := load(dir); err == nil {
		t.Fatal("unregistered file accepted")
	}
}

func TestPostgresFreshReplayAndRollback(t *testing.T) {
	dsn := os.Getenv("XIANGWAN_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not supplied")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// Never touch a caller's existing schema: dedicated per-test schema only.
	schema := "xiangwan_migration_test"
	_, err = conn.Exec(ctx, "CREATE SCHEMA "+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	_, err = conn.Exec(ctx, "SET search_path TO "+schema+", public")
	if err != nil {
		t.Fatal(err)
	}
	files, err := load("../../deploy/sql")
	if err != nil {
		t.Fatal(err)
	}
	first, err := apply(ctx, conn, files)
	if err != nil {
		t.Fatal(err)
	}
	if first != len(files) {
		t.Fatalf("applied %d/%d", first, len(files))
	}
	repeat, err := apply(ctx, conn, files)
	if err != nil || repeat != 0 {
		t.Fatalf("replay=%d err=%v", repeat, err)
	}
	bad := migration{"999_test_failure.up.sql", "CREATE TABLE rollback_probe(id INT); SELECT * FROM missing_table;", "test"}
	if _, err = apply(ctx, conn, []migration{bad}); err == nil {
		t.Fatal("failed statement accepted")
	}
	var exists bool
	if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name='rollback_probe')", schema).Scan(&exists); err != nil || exists {
		t.Fatalf("partial DDL survived: %v %v", exists, err)
	}
	if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)", bad.name).Scan(&exists); err != nil || exists {
		t.Fatalf("failed migration recorded: %v %v", exists, err)
	}
}
