// Package database provides the framework-owned SQL and GORM lifecycle.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/heavycaffeiner/hanami/health"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

type Config struct {
	Dialector     gorm.Dialector
	Pool          PoolConfig
	PingTimeout   time.Duration
	HealthTimeout time.Duration
	HealthName    string
}

func (config Config) pool() PoolConfig {
	return config.Pool
}

type runtime struct {
	db            *gorm.DB
	sqlDB         *sql.DB
	name          string
	pingTimeout   time.Duration
	healthTimeout time.Duration
	closeOnce     sync.Once
	closeErr      error
}

func newRuntime(config Config) (*runtime, error) {
	if config.Dialector == nil {
		return nil, errors.New("database dialector is required")
	}
	db, err := gorm.Open(config.Dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get database/sql pool: %w", err)
	}
	pool := config.pool()
	if pool.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(pool.MaxOpenConns)
	}
	if pool.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(pool.MaxIdleConns)
	}
	if pool.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(pool.ConnMaxLifetime)
	}
	if pool.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(pool.ConnMaxIdleTime)
	}
	name := config.HealthName
	if name == "" {
		name = "database"
	}
	return &runtime{db: db, sqlDB: sqlDB, name: name, pingTimeout: config.PingTimeout, healthTimeout: config.HealthTimeout}, nil
}

func (database *runtime) start(ctx context.Context) error {
	if database.pingTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, database.pingTimeout)
		defer cancel()
	}
	if err := database.sqlDB.PingContext(ctx); err != nil {
		return errors.Join(fmt.Errorf("database ping: %w", err), database.close())
	}
	return nil
}

func (database *runtime) stop(context.Context) error {
	return database.close()
}

func (database *runtime) close() error {
	database.closeOnce.Do(func() {
		database.closeErr = database.sqlDB.Close()
	})
	return database.closeErr
}
func (database *runtime) check(ctx context.Context) health.Result {
	if database.healthTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, database.healthTimeout)
		defer cancel()
	}
	if err := database.sqlDB.PingContext(ctx); err != nil {
		return health.Result{Status: health.StatusFail, Detail: "database ping failed", Err: err}
	}
	return health.Result{Status: health.StatusPass, Detail: "database is reachable"}
}

func Module(config Config) fx.Option {
	return fx.Module("hanami-database",
		fx.Provide(
			func(lifecycle fx.Lifecycle) (*runtime, error) {
				database, err := newRuntime(config)
				if err != nil {
					return nil, err
				}
				lifecycle.Append(fx.Hook{OnStart: database.start, OnStop: database.stop})
				return database, nil
			},
			func(database *runtime) *gorm.DB { return database.db },
			func(database *runtime) *sql.DB { return database.sqlDB },
			fx.Annotate(func(database *runtime) health.Check {
				return health.Check{Name: database.name, Kind: health.Readiness, Timeout: database.healthTimeout, Run: database.check}
			}, fx.ResultTags(`group:"hanami_health_checks"`)),
		),
	)
}
