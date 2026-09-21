// Package cli provides a small, application-neutral boundary around Kong.
package cli

import (
	"errors"
	"io"
	"os"

	"github.com/alecthomas/kong"
)

// Result is the outcome of one CLI parse and, when selected, command execution.
// Kong and Context are retained so callers can inspect the native parse state.
type Result struct {
	Kong    *kong.Kong
	Context *kong.Context
	Command string
	Error   error
}

// Run parses args against grammar and runs the selected command. It never exits
// the process. Parse and command errors are returned unchanged, so Kong parse
// errors retain errors.Is/errors.As behavior.
//
// options are applied to the Kong parser in order. bindings are available to
// command Run methods through Kong's normal injection mechanism.
func Run(grammar any, args []string, stdout, stderr io.Writer, options []kong.Option, bindings ...any) Result {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	exitCode := -1
	parserOptions := make([]kong.Option, 0, len(options)+2)
	parserOptions = append(parserOptions, options...)
	// These are deliberately last: a caller-provided Exit option must not be
	// able to reintroduce process termination into this boundary.
	parserOptions = append(parserOptions,
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) { exitCode = code }),
	)

	parser, err := kong.New(grammar, parserOptions...)
	if err != nil {
		return Result{Error: err}
	}

	ctx, err := parser.Parse(args)
	if ctx == nil {
		var parseErr *kong.ParseError
		if errors.As(err, &parseErr) {
			ctx = parseErr.Context
		}
	}
	result := Result{Kong: parser, Context: ctx, Error: err}
	if ctx != nil {
		result.Command = ctx.Command()
	}

	// Kong's help flag prints during BeforeReset and then calls Exit(0). The
	// recorder lets us preserve that behavior without stopping the process.
	if exitCode == 0 {
		result.Error = nil
		return result
	}
	if err != nil {
		// Render parse diagnostics through Kong while retaining the original
		// error in Result.Error.
		parser.FatalIfErrorf(err)
		return result
	}

	result.Error = ctx.Run(bindings...)
	return result
}

// DefaultArgs returns process arguments without making Run depend on os.Args.
// It is a convenience for executable adapters that want explicit arguments in
// tests while using process arguments in production.
func DefaultArgs() []string {
	if len(os.Args) < 2 {
		return nil
	}
	return append([]string(nil), os.Args[1:]...)
}
