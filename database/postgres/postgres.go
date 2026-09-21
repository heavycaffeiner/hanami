// Package postgres provides the PostgreSQL GORM dialect integration.
package postgres

import (
	"time"

	"github.com/heavycaffeiner/hanami/database"
	"go.uber.org/fx"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Config struct {
	DSN                  string
	PreferSimpleProtocol bool
	DriverName           string
	Pool                 database.PoolConfig
	PingTimeout          time.Duration
	HealthTimeout        time.Duration
	HealthName           string
}

func (config Config) Dialector() gorm.Dialector {
	return gormpostgres.New(gormpostgres.Config{DSN: config.DSN, PreferSimpleProtocol: config.PreferSimpleProtocol, DriverName: config.DriverName})
}

func Module(config Config) fx.Option {
	return database.Module(database.Config{
		Dialector: config.Dialector(), Pool: config.Pool,
		PingTimeout: config.PingTimeout, HealthTimeout: config.HealthTimeout, HealthName: config.HealthName,
	})
}
