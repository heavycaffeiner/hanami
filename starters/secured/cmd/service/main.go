//go:build linux

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/heavycaffeiner/hanami"
	"github.com/heavycaffeiner/hanami/process"
	securitylinux "github.com/heavycaffeiner/hanami/security/linux"
	"go.uber.org/fx"
)

type applicationConfig struct {
	AllowedDirectory string
}

func main() {
	hanami.Main(hanami.Spec[applicationConfig]{
		Name:    "hanami-secured-starter",
		Load:    loadConfig,
		Modules: applicationModules,
	},
		securitylinux.WithPolicy(buildPolicy),
		hanami.WithTimeouts[applicationConfig](hanami.Timeouts{Startup: 15 * time.Second, Shutdown: 10 * time.Second}),
	)
}

func loadConfig(context.Context) (applicationConfig, error) {
	path := os.Getenv("HANAMI_SECURED_DIRECTORY")
	if path == "" {
		path = os.TempDir()
	}
	info, err := os.Stat(path)
	if err != nil {
		return applicationConfig{}, fmt.Errorf("inspect allowed directory: %w", err)
	}
	if !info.IsDir() {
		return applicationConfig{}, fmt.Errorf("allowed path is not a directory: %s", path)
	}
	return applicationConfig{AllowedDirectory: path}, nil
}

func buildPolicy(config applicationConfig) (securitylinux.Policy, error) {
	grants := []securitylinux.Grant{{
		Path: config.AllowedDirectory,
		Access: securitylinux.RightReadFile | securitylinux.RightReadDirectory |
			securitylinux.RightWriteFile | securitylinux.RightMakeDirectory |
			securitylinux.RightMakeRegular | securitylinux.RightRemoveDirectory |
			securitylinux.RightRemoveFile | securitylinux.RightTruncate |
			securitylinux.RightRefer,
	}}
	if executable, err := os.Executable(); err == nil {
		grants = append(grants, securitylinux.Grant{
			Path:   filepath.Clean(executable),
			Access: securitylinux.RightReadFile | securitylinux.RightExecute,
		})
	}
	return securitylinux.Policy{
		Mode:         securitylinux.ModePreferred,
		Grants:       grants,
		DenySyscalls: []string{"ptrace", "mount", "bpf"},
		Landlock:     true,
		Seccomp:      true,
	}, nil
}

func applicationModules(applicationConfig) fx.Option {
	return fx.Invoke(func(status securitylinux.Status, logger *slog.Logger, controller *process.Controller) {
		logger.Info("Security enforcement completed",
			slog.Bool("enforced", status.Enforced),
			slog.Bool("landlock", status.Landlock.Verified),
			slog.Bool("seccomp", status.Seccomp.Verified),
			slog.String("architecture", status.Architecture),
		)
		controller.RequestStop(process.Request{Reason: process.StopReasonRequested})
	})
}
