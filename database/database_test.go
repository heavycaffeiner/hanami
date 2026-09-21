package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/heavycaffeiner/hanami/database"
	"github.com/heavycaffeiner/hanami/database/sqlite"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"gorm.io/gorm"
)

func TestSQLiteModuleExposesNativePoolsAndCloses(t *testing.T) {
	var gormDB *gorm.DB
	var sqlDB *sql.DB
	app := fxtest.New(t,
		sqlite.Module(sqlite.Config{DSN: "file:test-lifecycle?mode=memory&cache=shared"}),
		fx.Populate(&gormDB, &sqlDB),
	)
	app.RequireStart()
	if gormDB == nil || sqlDB == nil {
		t.Fatal("database providers were not populated")
	}
	app.RequireStop()
	if err := sqlDB.PingContext(context.Background()); err == nil {
		t.Fatal("expected pool to be closed")
	}
}

func TestConfigRequiresDialector(t *testing.T) {
	var sqlDB *sql.DB
	app := fx.New(
		database.Module(database.Config{}),
		fx.Populate(&sqlDB),
	)
	if app.Err() == nil {
		t.Fatal("expected missing dialector error")
	}
}
