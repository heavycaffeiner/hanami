package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type testDependency struct {
	Value string
}

type testServe struct{}

func (testServe) Run(dep *testDependency) error {
	dep.Value = "served"
	return nil
}

type testMigrateUp struct{}

func (testMigrateUp) Run(dep *testDependency) error {
	dep.Value = "up"
	return nil
}

type testMigrate struct {
	Up     testMigrateUp `cmd:""`
	Down   struct{}      `cmd:""`
	Status struct{}      `cmd:""`
}

type testVersion struct{}

func (testVersion) Run() error { return nil }

type testGrammar struct {
	Serve   testServe   `cmd:""`
	Migrate testMigrate `cmd:""`
	Version testVersion `cmd:""`
}

var errCommand = errors.New("command failed")

type testFail struct{}

func (testFail) Run() error { return errCommand }

func TestRunParsesAndRunsApplicationCommandWithBinding(t *testing.T) {
	dep := &testDependency{}
	result := Run(&testGrammar{}, []string{"serve"}, &bytes.Buffer{}, &bytes.Buffer{}, nil, dep)
	if result.Error != nil {
		t.Fatalf("Run returned error: %v", result.Error)
	}
	if result.Command != "serve" {
		t.Fatalf("unexpected command: %q", result.Command)
	}
	if dep.Value != "served" {
		t.Fatalf("binding was not injected: %q", dep.Value)
	}
	if result.Kong == nil || result.Context == nil {
		t.Fatal("expected native Kong and Context values")
	}
}

func TestRunCapturesHelpWithoutExiting(t *testing.T) {
	var stdout, stderr bytes.Buffer
	result := Run(&testGrammar{}, []string{"--help"}, &stdout, &stderr, nil)
	if result.Error != nil {
		t.Fatalf("help returned error: %v", result.Error)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Fatalf("help output missing commands: %q", stdout.String())
	}
}

func TestRunCapturesParseErrorWithoutExiting(t *testing.T) {
	var stdout, stderr bytes.Buffer
	result := Run(&testGrammar{}, []string{"unknown"}, &stdout, &stderr, nil)
	if result.Error == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Fatalf("error output missing diagnostic: %q", stderr.String())
	}
}

func TestRunPreservesCommandError(t *testing.T) {
	grammar := struct {
		Fail testFail `cmd:""`
	}{}
	result := Run(&grammar, []string{"fail"}, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if !errors.Is(result.Error, errCommand) {
		t.Fatalf("command error was not preserved: %v", result.Error)
	}
}
