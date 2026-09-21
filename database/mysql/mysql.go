// Package mysql provides the MySQL GORM dialect integration.
package mysql

import (
	"time"

	"github.com/heavycaffeiner/hanami/database"
	"go.uber.org/fx"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type Config struct {
	DSN                       string
	DefaultStringSize         uint
	DisableDatetimePrecision  bool
	DontSupportRenameIndex    bool
	DontSupportRenameColumn   bool
	SkipInitializeWithVersion bool
	Pool                      database.PoolConfig
	PingTimeout               time.Duration
	HealthTimeout             time.Duration
	HealthName                string
}

func (config Config) Dialector() gorm.Dialector {
	return gormmysql.New(gormmysql.Config{
		DSN: config.DSN, DefaultStringSize: config.DefaultStringSize,
		DisableDatetimePrecision:  config.DisableDatetimePrecision,
		DontSupportRenameIndex:    config.DontSupportRenameIndex,
		DontSupportRenameColumn:   config.DontSupportRenameColumn,
		SkipInitializeWithVersion: config.SkipInitializeWithVersion,
	})
}

func Module(config Config) fx.Option {
	return database.Module(database.Config{
		Dialector: config.Dialector(), Pool: config.Pool,
		PingTimeout: config.PingTimeout, HealthTimeout: config.HealthTimeout, HealthName: config.HealthName,
	})
}
