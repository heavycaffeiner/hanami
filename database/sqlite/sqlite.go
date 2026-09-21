// Package sqlite provides the pure-Go SQLite GORM dialect integration.
package sqlite

import (
	"net/url"
	"strconv"
	"time"

	"github.com/heavycaffeiner/hanami/database"
	libsqlite "github.com/libtnb/sqlite"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

type Config struct {
	DSN           string
	WAL           bool
	BusyTimeout   time.Duration
	ForeignKeys   bool
	Pool          database.PoolConfig
	PingTimeout   time.Duration
	HealthTimeout time.Duration
	HealthName    string
}

func (config Config) Dialector() gorm.Dialector {
	return libsqlite.Open(config.dsn())
}

func (config Config) dsn() string {
	if config.DSN == "" {
		return config.DSN
	}
	parsed, err := url.Parse(config.DSN)
	if err != nil {
		return config.DSN
	}
	query := parsed.Query()
	if config.WAL {
		query.Add("_pragma", "journal_mode(WAL)")
	}
	if config.BusyTimeout > 0 {
		query.Add("_pragma", "busy_timeout("+strconv.FormatInt(config.BusyTimeout.Milliseconds(), 10)+")")
	}
	if config.ForeignKeys {
		query.Add("_pragma", "foreign_keys(1)")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func Module(config Config) fx.Option {
	return database.Module(database.Config{
		Dialector: config.Dialector(), Pool: config.Pool,
		PingTimeout: config.PingTimeout, HealthTimeout: config.HealthTimeout, HealthName: config.HealthName,
	})
}
