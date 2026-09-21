package migration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/fstest"

	_ "github.com/libtnb/sqlite"
	"github.com/pressly/goose/v3"
)

const createMigration = `-- +goose Up
CREATE TABLE example (id INTEGER PRIMARY KEY);

-- +goose Down
DROP TABLE example;
`

func TestNewReusesSharedPool(t *testing.T) {
	db := openTestDB(t)
	manager, err := New(Config{Policy: ValidateOnly, Dialect: goose.DialectSQLite3, FS: migrationFS(createMigration)}, db)
	if err != nil {
		t.Fatal(err)
	}
	if manager.DB() != db {
		t.Fatalf("manager database %p, want %p", manager.DB(), db)
	}
	if manager.Provider() == nil {
		t.Fatal("provider is nil")
	}
}

func TestDisabledPolicyNoOp(t *testing.T) {
	manager, err := New(Config{Policy: Disabled}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Condition().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Provider() != nil {
		t.Fatal("disabled manager has provider")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateOnlyPendingRefusal(t *testing.T) {
	manager := newTestManager(t, ValidateOnly, createMigration)
	if err := manager.Condition().Run(context.Background()); !errors.Is(err, ErrPendingMigrations) {
		t.Fatalf("condition error = %v, want ErrPendingMigrations", err)
	}
}

func TestMigrateBeforeStartAppliesBeforeAdmission(t *testing.T) {
	manager := newTestManager(t, MigrateBeforeStart, createMigration)
	if err := manager.Condition().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var table string
	if err := manager.DB().QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'example'").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != "example" {
		t.Fatalf("table = %q, want example", table)
	}
	pending, err := manager.HasPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("migration remains pending after startup condition")
	}
}

func TestMigrationFailureBlocksAdmission(t *testing.T) {
	manager := newTestManager(t, MigrateBeforeStart, `-- +goose Up
THIS IS NOT VALID SQL;

-- +goose Down
SELECT 1;
`)
	if err := manager.Condition().Run(context.Background()); err == nil {
		t.Fatal("migration condition succeeded for invalid SQL")
	}
}

func TestCloseDoesNotCloseDatabase(t *testing.T) {
	db := openTestDB(t)
	manager, err := New(Config{Policy: Disabled}, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("database was closed by migration cleanup: %v", err)
	}
}

func newTestManager(t *testing.T, policy Policy, migration string) *Manager {
	t.Helper()
	db := openTestDB(t)
	manager, err := New(Config{Policy: policy, Dialect: goose.DialectSQLite3, FS: migrationFS(migration)}, db)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func migrationFS(sql string) fstest.MapFS {
	return fstest.MapFS{"001_init.sql": &fstest.MapFile{Data: []byte(sql)}}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:migration-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	return db
}
