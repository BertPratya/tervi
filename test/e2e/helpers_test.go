package e2e

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestMachineID(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machine-id")
	if err := os.WriteFile(path, []byte("1023456789abcdef0123456789abcdef\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type workerRun struct {
	input  *io.PipeWriter
	stdout *safeBuffer
	stderr *safeBuffer
	done   chan int
}

func waitFor(t *testing.T, output *safeBuffer, text string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), text) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("output did not contain %q before timeout: %q", text, output.String())
}

func writeCode(t *testing.T, run *workerRun, code string, timeout time.Duration) {
	t.Helper()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(run.input, code+"\n")
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write code to worker: %v", err)
		}
	case <-time.After(timeout):
		_ = run.input.Close()
		t.Fatalf("worker did not read code within %s", timeout)
	}
}

func awaitRun(t *testing.T, run *workerRun, timeout time.Duration) int {
	t.Helper()
	select {
	case code := <-run.done:
		_ = run.input.Close()
		return code
	case <-time.After(timeout):
		_ = run.input.Close()
		t.Fatalf("worker did not exit; stdout=%q stderr=%q", run.stdout.String(), run.stderr.String())
		return -1
	}
}
