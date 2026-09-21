// Package migration integrates Goose migrations with Hanami startup.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/heavycaffeiner/hanami/bootstrap"
	"github.com/pressly/goose/v3"
	"go.uber.org/fx"
)

// Policy controls how schema migrations participate in startup.
type Policy uint8

const (
	// Disabled leaves the database untouched and contributes no startup work.
	Disabled Policy = iota
	// ValidateOnly refuses startup when migrations are pending.
	ValidateOnly
	// MigrateBeforeStart applies all pending migrations before startup admission.
	MigrateBeforeStart
)

func (p Policy) String() string {
	switch p {
	case Disabled:
		return "disabled"
	case ValidateOnly:
		return "validate_only"
	case MigrateBeforeStart:
		return "migrate_before_start"
	default:
		return fmt.Sprintf("unknown(%d)", p)
	}
}

var (
	// ErrPendingMigrations identifies a validate-only startup blocked by pending work.
	ErrPendingMigrations = errors.New("pending migrations")
)

// Config describes a Goose provider and its startup policy.
type Config struct {
	Policy  Policy
	Dialect goose.Dialect
	FS      fs.FS
	Options []goose.ProviderOption
}

// Manager owns a migration provider but never owns its database connection.
type Manager struct {
	policy   Policy
	db       *sql.DB
	provider *goose.Provider
}

// New constructs a migration manager over db. The database pool remains owned by
// the caller; neither Manager nor its Provider is closed by this package.
func New(config Config, db *sql.DB) (*Manager, error) {
	if config.Policy > MigrateBeforeStart {
		return nil, fmt.Errorf("unknown migration policy %d", config.Policy)
	}
	manager := &Manager{policy: config.Policy, db: db}
	if config.Policy == Disabled {
		return manager, nil
	}
	if db == nil {
		return nil, errors.New("migration database is nil")
	}
	provider, err := goose.NewProvider(config.Dialect, db, config.FS, config.Options...)
	if err != nil {
		return nil, fmt.Errorf("create goose provider: %w", err)
	}
	manager.provider = provider
	return manager, nil
}

// Policy reports the configured startup policy.
func (m *Manager) Policy() Policy {
	if m == nil {
		return Disabled
	}
	return m.policy
}

// DB returns the exact database pool supplied to New.
func (m *Manager) DB() *sql.DB {
	if m == nil {
		return nil
	}
	return m.db
}

// Provider returns the native Goose provider, or nil when migrations are disabled.
func (m *Manager) Provider() *goose.Provider {
	if m == nil {
		return nil
	}
	return m.provider
}

// HasPending reports whether the provider has unapplied migrations. Disabled
// managers report false without touching the database.
func (m *Manager) HasPending(ctx context.Context) (bool, error) {
	if m == nil || m.policy == Disabled {
		return false, nil
	}
	return m.provider.HasPending(ctx)
}

// Status returns the native Goose migration status. Disabled managers return an
// empty status without touching the database.
func (m *Manager) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	if m == nil || m.policy == Disabled {
		return nil, nil
	}
	return m.provider.Status(ctx)
}

// Validate checks that no migrations are pending. It is useful to CLI callers
// and is also the validate-only startup operation.
func (m *Manager) Validate(ctx context.Context) error {
	if m == nil || m.policy == Disabled {
		return nil
	}
	pending, err := m.provider.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check pending migrations: %w", err)
	}
	if pending {
		return ErrPendingMigrations
	}
	return nil
}

// Migrate applies every pending migration. It is useful to CLI callers and is
// also the migrate-before-start startup operation.
func (m *Manager) Migrate(ctx context.Context) ([]*goose.MigrationResult, error) {
	if m == nil || m.policy == Disabled {
		return nil, nil
	}
	results, err := m.provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return results, nil
}

// Close intentionally does nothing. The injected *sql.DB is owned by the
// database integration and must outlive this package.
func (m *Manager) Close() error { return nil }

// Condition returns the startup barrier for the configured policy.
func (m *Manager) Condition() bootstrap.Condition {
	return bootstrap.Condition{
		Name: "migrations",
		Run: func(ctx context.Context) error {
			switch m.Policy() {
			case Disabled:
				return nil
			case ValidateOnly:
				return m.Validate(ctx)
			case MigrateBeforeStart:
				_, err := m.Migrate(ctx)
				return err
			default:
				return fmt.Errorf("unknown migration policy %d", m.Policy())
			}
		},
	}
}

// Module registers a migration manager and its startup condition. Enabled
// policies require an injected *sql.DB; disabled mode does not.
func Module(config Config) fx.Option {
	condition := fx.Provide(fx.Annotate(
		func(manager *Manager) bootstrap.Condition {
			return manager.Condition()
		},
		fx.ResultTags(`group:"`+bootstrap.StartupConditionGroup+`"`),
	))
	if config.Policy == Disabled {
		return fx.Module("hanami-migration",
			fx.Provide(func() (*Manager, error) { return New(config, nil) }),
			condition,
			fx.Provide(func(manager *Manager) *goose.Provider { return manager.Provider() }),
		)
	}
	return fx.Module(
		"hanami-migration",
		fx.Provide(func(db *sql.DB) (*Manager, error) {
			return New(config, db)
		}),
		condition,
		fx.Provide(func(manager *Manager) *goose.Provider {
			return manager.Provider()
		}),
	)
}
