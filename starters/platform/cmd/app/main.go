package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/heavycaffeiner/hanami"
	"github.com/heavycaffeiner/hanami/cli"
	"github.com/heavycaffeiner/hanami/migration"
	"github.com/heavycaffeiner/hanami/starters/platform/app"
	_ "github.com/libtnb/sqlite"
	"github.com/pressly/goose/v3"
)

type CLI struct {
	Serve   ServeCommand   `cmd:""`
	Migrate MigrateCommand `cmd:""`
	Version VersionCommand `cmd:""`
}
type ServeCommand struct{}
type MigrateCommand struct {
	Up     MigrateUpCommand     `cmd:""`
	Status MigrateStatusCommand `cmd:""`
}
type MigrateUpCommand struct{}
type MigrateStatusCommand struct{}
type VersionCommand struct{}

func (ServeCommand) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, err := hanami.Run(ctx, hanami.Spec[app.Config]{
		Name:    "hanami-platform",
		Load:    app.LoadConfig,
		Modules: app.Modules,
	}, hanami.WithTimeouts[app.Config](hanami.Timeouts{Startup: 15 * time.Second, Shutdown: 30 * time.Second}))
	return err
}
func (MigrateUpCommand) Run() error     { return runMigration(os.Stdout, true) }
func (MigrateStatusCommand) Run() error { return runMigration(os.Stdout, false) }
func (VersionCommand) Run() error       { _, err := fmt.Fprintln(os.Stdout, app.Version); return err }

func runMigration(out io.Writer, up bool) error {
	cfg, err := app.LoadConfig(context.Background())
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	manager, err := migration.New(migration.Config{Policy: migration.MigrateBeforeStart, Dialect: goose.DialectSQLite3, FS: app.MigrationFiles}, db)
	if err != nil {
		return err
	}
	if up {
		_, err = manager.Migrate(context.Background())
		return err
	}
	statuses, err := manager.Status(context.Background())
	if err != nil {
		return err
	}
	for _, status := range statuses {
		if _, err := fmt.Fprintf(out, "%s %s\n", status.State, status.Source.Path); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	result := cli.Run(&CLI{}, cli.DefaultArgs(), os.Stdout, os.Stderr, []kong.Option{})
	if result.Error != nil {
		_, _ = fmt.Fprintln(os.Stderr, result.Error)
		os.Exit(1)
	}
}
