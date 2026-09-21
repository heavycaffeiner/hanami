package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-co-op/gocron/v2"
	"github.com/heavycaffeiner/hanami/api"
	"github.com/heavycaffeiner/hanami/bootstrap"
	hsqlite "github.com/heavycaffeiner/hanami/database/sqlite"
	"github.com/heavycaffeiner/hanami/health"
	"github.com/heavycaffeiner/hanami/migration"
	_ "github.com/libtnb/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(hsqlite.Config{DSN: dsn, ForeignKeys: true}.Dialector(), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := migration.New(migration.Config{Policy: migration.MigrateBeforeStart, Dialect: goose.DialectSQLite3, FS: MigrationFiles}, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestMigrateBeforeAdmissionCreatesNotesTable(t *testing.T) {
	db, err := sql.Open("sqlite", "file:platform-migration-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := migration.New(migration.Config{Policy: migration.MigrateBeforeStart, Dialect: goose.DialectSQLite3, FS: MigrationFiles}, db)
	if err != nil {
		t.Fatal(err)
	}
	admission := bootstrap.NewAdmission()
	if err := manager.Condition().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='notes'").Scan(&name); err != nil {
		t.Fatalf("migration did not create notes table: %v", err)
	}
	if admission.IsOpen() {
		t.Fatal("admission opened before application startup")
	}
}

func TestTypedNoteHTTPCreateAndList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	admission := bootstrap.NewAdmission()
	admission.Open()
	registry, err := health.New(health.Params{}, admission)
	if err != nil {
		t.Fatal(err)
	}
	apiServer := api.New(engine, huma.DefaultConfig("platform-test", "1"), "/v1")
	service := NewNoteService(NewNoteRepository(testDB(t)))
	registerRoutes(engine, registry, apiServer, service, slog.Default())

	create := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/notes", strings.NewReader(`{"title":"first","content":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(create, req)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", create.Code, create.Body.String())
	}
	var created Note
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "first" || created.ID == 0 {
		t.Fatalf("unexpected created note: %+v", created)
	}

	list := httptest.NewRecorder()
	engine.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/notes", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", list.Code, list.Body.String())
	}
	var output struct {
		Notes []Note `json:"notes"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Notes) != 1 || output.Notes[0].Content != "hello" {
		t.Fatalf("unexpected list response: %+v", output)
	}
}

func TestHeartbeatUsesNativeGocronRegistration(t *testing.T) {
	scheduler, err := gocron.NewScheduler()
	if err != nil {
		t.Fatal(err)
	}
	defer scheduler.Shutdown()
	cfg := Config{}
	cfg.Scheduler.Interval = time.Hour
	job := heartbeatJob(slog.Default(), cfg)
	if err := job.Register(scheduler); err != nil {
		t.Fatal(err)
	}
	if got := len(scheduler.Jobs()); got != 1 {
		t.Fatalf("registered jobs = %d, want 1", got)
	}
}
