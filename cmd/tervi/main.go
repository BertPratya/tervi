package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bertpratya/tervi/internal/worker/cli"
	"github.com/bertpratya/tervi/internal/worker/control"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/pair"
	"github.com/bertpratya/tervi/internal/worker/service"
)

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stdout, "✗ Can't find this user's configuration folder.")
		os.Exit(1)
	}
	programPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stdout, "✗ Can't find tervi's program file.")
		os.Exit(1)
	}
	runtimeDir, _ := control.RuntimeDir()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	env := cli.Env{
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		Store:         credential.NewStore(credential.NewKeyringBackend()),
		StateDir:      filepath.Join(configDir, "tervi"),
		Runner:        service.ExecRunner{},
		ProgramPath:   programPath,
		ServiceDir:    filepath.Join(configDir, "systemd", "user"),
		ControlClient: control.Client{RuntimeDir: runtimeDir},
	}
	workerFlow := pair.New(pair.Options{
		RequestTimeout: 10 * time.Second,
		PollInterval:   2 * time.Second,
		Now:            time.Now,
	})
	code := cli.Run(ctx, os.Args[1:], env, workerFlow)
	stop()
	os.Exit(code)
}
