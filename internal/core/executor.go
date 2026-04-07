package core

import "log/slog"

// Executor handles state-mutating actions, enforcing Dry Run rules and standardizing logs.
type Executor struct {
	DryRun bool
}

// NewExecutor creates a new action executor.
func NewExecutor(dryRun bool) *Executor {
	return &Executor{DryRun: dryRun}
}

// Run executes the given function if DryRun is false. It handles all logging automatically.
// The args parameter uses variadic key-value pairs, matching standard slog syntax.
func (e *Executor) Run(action string, fn func() error, args ...any) error {
	if e.DryRun {
		slog.Info("[DRY RUN] Would "+action, args...)
		return nil
	}

	if err := fn(); err != nil {
		// Assign the append result back to prevent underlying array mutation bugs.
		args = append(args, "error", err)
		slog.Error("Failed to "+action, args...)
		return err
	}

	slog.Info("Successfully executed: "+action, args...)
	return nil
}
