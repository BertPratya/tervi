package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

func runLogs(ctx context.Context, args []string, env Env) int {
	follow := false
	if len(args) == 1 && args[0] == "-f" {
		follow = true
	} else if len(args) != 0 {
		fmt.Fprintln(env.Stderr, usageLine)
		return 2
	}
	if env.Runner == nil {
		return missingSetting(env.Stdout, "Runner")
	}
	commandArgs := []string{"--user", "-u", "tervi", "-o", "cat", "--no-pager"}
	if follow {
		commandArgs = append(commandArgs, "-f")
	}
	err := env.Runner.Stream(ctx, env.Stdout, "journalctl", commandArgs...)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return 0
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return 1
		}
		fmt.Fprintf(env.Stdout, "✗ Can't read the log: %s\n", err.Error())
		return 1
	}
	return 0
}
