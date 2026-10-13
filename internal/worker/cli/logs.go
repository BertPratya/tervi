package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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
	var output io.Writer = env.Stdout
	var redact *credentialRedactingWriter
	if entry, exists, err := env.Store.Get(); err == nil && exists {
		secret := entry.Credential.Reveal()
		if secret != "" {
			redact = &credentialRedactingWriter{out: env.Stdout, secret: []byte(secret)}
			output = redact
		}
	}
	err := env.Runner.Stream(ctx, output, "journalctl", commandArgs...)
	if redact != nil {
		if flushErr := redact.Flush(); err == nil {
			err = flushErr
		}
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return 0
	}
	if err != nil {
		message := strings.TrimSpace(err.Error())
		if redact != nil {
			message = strings.ReplaceAll(message, string(redact.secret), "[redacted]")
		}
		fmt.Fprintf(env.Stdout, "✗ Can't read the log: %s\n", message)
		return 1
	}
	return 0
}

type credentialRedactingWriter struct {
	out     io.Writer
	secret  []byte
	pending []byte
}

func (writer *credentialRedactingWriter) Write(chunk []byte) (int, error) {
	data := append(writer.pending, chunk...)
	cutoff := len(data) - len(writer.secret) + 1
	if cutoff <= 0 {
		writer.pending = append(writer.pending[:0], data...)
		return len(chunk), nil
	}
	var safe bytes.Buffer
	index := 0
	for index < cutoff {
		if bytes.HasPrefix(data[index:], writer.secret) {
			safe.WriteString("[redacted]")
			index += len(writer.secret)
		} else {
			safe.WriteByte(data[index])
			index++
		}
	}
	writer.pending = append(writer.pending[:0], data[index:]...)
	if _, err := writer.out.Write(safe.Bytes()); err != nil {
		return 0, err
	}
	return len(chunk), nil
}

func (writer *credentialRedactingWriter) Flush() error {
	if len(writer.pending) == 0 {
		return nil
	}
	_, err := writer.out.Write(writer.pending)
	writer.pending = nil
	return err
}
