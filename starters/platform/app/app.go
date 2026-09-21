package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-co-op/gocron/v2"
	"github.com/heavycaffeiner/hanami/api"
	"github.com/heavycaffeiner/hanami/config"
	"github.com/heavycaffeiner/hanami/database"
	hsqlite "github.com/heavycaffeiner/hanami/database/sqlite"
	hgin "github.com/heavycaffeiner/hanami/gin"
	"github.com/heavycaffeiner/hanami/health"
	hhttp "github.com/heavycaffeiner/hanami/http"
	"github.com/heavycaffeiner/hanami/migration"
	"github.com/heavycaffeiner/hanami/scheduler"
	_ "github.com/libtnb/sqlite"
	"github.com/pressly/goose/v3"
	"go.uber.org/fx"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
)

//go:embed migrations/*.sql
var Migrations embed.FS

var MigrationFiles fs.FS

func init() {
	var err error
	MigrationFiles, err = fs.Sub(Migrations, "migrations")
	if err != nil {
		panic(err)
	}
}

const Version = "platform-reference-1.0.0"

type Config struct {
	HTTP struct {
		Address           string        `mapstructure:"address"`
		ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
		IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
		MaxHeaderBytes    int           `mapstructure:"max_header_bytes"`
	} `mapstructure:"http"`
	Database struct {
		DSN string `mapstructure:"dsn"`
		WAL bool   `mapstructure:"wal"`
	} `mapstructure:"database"`
	Migration struct {
		Policy string `mapstructure:"policy"`
	} `mapstructure:"migration"`
	Scheduler struct {
		Interval time.Duration `mapstructure:"interval"`
	} `mapstructure:"scheduler"`
}

func (c Config) Validate() error {
	if err := (hhttp.ServerConfig{Address: c.HTTP.Address, ReadHeaderTimeout: c.HTTP.ReadHeaderTimeout, IdleTimeout: c.HTTP.IdleTimeout, MaxHeaderBytes: c.HTTP.MaxHeaderBytes}).Validate(); err != nil {
		return err
	}
	if c.Database.DSN == "" {
		return errors.New("database dsn is empty")
	}
	if c.Migration.Policy != "migrate-before-start" && c.Migration.Policy != "validate-only" && c.Migration.Policy != "disabled" {
		return fmt.Errorf("unknown migration policy %q", c.Migration.Policy)
	}
	if c.Scheduler.Interval <= 0 {
		return errors.New("scheduler interval must be positive")
	}
	return nil
}

func defaults() map[string]any {
	return map[string]any{"http.address": "127.0.0.1:8080", "http.read_header_timeout": "5s", "http.idle_timeout": "60s", "http.max_header_bytes": 1048576, "database.dsn": "file:platform.db", "database.wal": true, "migration.policy": "migrate-before-start", "scheduler.interval": "1h"}
}
func LoadConfig(ctx context.Context) (Config, error) {
	return config.Load[Config](ctx, config.Source{EnvPrefix: "HANAMI_PLATFORM", Defaults: defaults()})
}
func LoadConfigFile(ctx context.Context, path string) (Config, error) {
	return config.Load[Config](ctx, config.Source{File: path, EnvPrefix: "HANAMI_PLATFORM", Defaults: defaults()})
}
func MigrationPolicy(value string) migration.Policy {
	switch value {
	case "disabled":
		return migration.Disabled
	case "validate-only":
		return migration.ValidateOnly
	default:
		return migration.MigrateBeforeStart
	}
}

func Modules(cfg Config) fx.Option {
	return fx.Options(
		health.Module(),
		hsqlite.Module(hsqlite.Config{DSN: cfg.Database.DSN, WAL: cfg.Database.WAL, ForeignKeys: true, Pool: database.PoolConfig{MaxOpenConns: 1}, PingTimeout: 5 * time.Second, HealthTimeout: 2 * time.Second, HealthName: "database"}),
		migration.Module(migration.Config{Policy: MigrationPolicy(cfg.Migration.Policy), Dialect: goose.DialectSQLite3, FS: MigrationFiles}),
		hgin.Module(hgin.Config{Mode: gin.ReleaseMode, Preset: &hgin.PresetConfig{ServiceName: "hanami-platform", SecurityHeaders: true}}),
		api.Module(api.Config{Huma: huma.DefaultConfig("Hanami Platform", Version), Prefix: "/v1"}),
		scheduler.Module(scheduler.Config{ShutdownTimeout: 5 * time.Second, HealthTimeout: 2 * time.Second, HealthName: "scheduler"}),
		hhttp.Module(hhttp.ServerConfig{Address: cfg.HTTP.Address, ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout, IdleTimeout: cfg.HTTP.IdleTimeout, MaxHeaderBytes: cfg.HTTP.MaxHeaderBytes}),
		fx.Provide(NewNoteRepository, NewNoteService),
		fx.Provide(fx.Annotate(heartbeatJob, fx.ResultTags(`group:"hanami_scheduler_jobs"`))),
		fx.Invoke(registerRoutes),
	)
}

func heartbeatJob(logger *slog.Logger, cfg Config) scheduler.Job {
	return scheduler.Job{Name: "platform-heartbeat", Register: func(s gocron.Scheduler) error {
		_, err := s.NewJob(gocron.DurationJob(cfg.Scheduler.Interval), gocron.NewTask(func(ctx context.Context) { logger.InfoContext(ctx, "platform heartbeat") }))
		return err
	}}
}

func registerRoutes(engine *gin.Engine, registry *health.Registry, api huma.API, service *NoteService, logger *slog.Logger) {
	hgin.MountHealth(engine, registry)
	huma.Register(api, huma.Operation{OperationID: "create-note", Method: http.MethodPost, Path: "/notes", DefaultStatus: http.StatusCreated, Summary: "Create a note"}, func(ctx context.Context, input *CreateNoteInput) (*NoteOutput, error) {
		note, err := service.Create(ctx, input.Body.Title, input.Body.Content)
		if err != nil {
			return nil, huma.Error500InternalServerError("create note failed", err)
		}
		logger.InfoContext(ctx, "note created", "id", note.ID)
		return &NoteOutput{Body: note}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "list-notes", Method: http.MethodGet, Path: "/notes", Summary: "List notes"}, func(ctx context.Context, _ *ListNotesInput) (*ListNotesOutput, error) {
		notes, err := service.List(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list notes failed", err)
		}
		return &ListNotesOutput{Body: struct {
			Notes []Note `json:"notes"`
		}{Notes: notes}}, nil
	})
}
