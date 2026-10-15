package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/worker/credential"
)

type unpairRecorder struct {
	answer       UnpairAnswer
	remove       *RemoveFailure
	linger       bool
	entry        credential.Entry
	tellCalls    int
	removeCalls  int
	lingerCalls  int
	localEnvSeen bool
}

func (recorder *unpairRecorder) TellServer(_ context.Context, entry credential.Entry) UnpairAnswer {
	recorder.tellCalls++
	recorder.entry = entry
	return recorder.answer
}

func (recorder *unpairRecorder) RemoveLocal(_ context.Context, env Env) *RemoveFailure {
	recorder.removeCalls++
	recorder.localEnvSeen = env.StateDir != ""
	return recorder.remove
}

func (recorder *unpairRecorder) LingerOn(context.Context, Env) bool {
	recorder.lingerCalls++
	return recorder.linger
}

func unpairCommandEnv(t *testing.T, input io.Reader, recorder *unpairRecorder) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	env, _, stdout, stderr := commandEnv(t, &commandRunner{}, &commandControl{}, true)
	env.Stdin = input
	env.Unpairer = recorder
	return env, stdout, stderr
}

func TestUnpairAsks(t *testing.T) {
	cases := []struct {
		name        string
		answer      string
		continueRun bool
	}{
		{name: "lowercase y", answer: "y\n", continueRun: true},
		{name: "lowercase y at end of input", answer: "y", continueRun: true},
		{name: "title case yes", answer: "Yes\n", continueRun: true},
		{name: "trimmed yes", answer: " yes \n", continueRun: true},
		{name: "no", answer: "n\n"},
		{name: "empty", answer: "\n"},
		{name: "end of input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &unpairRecorder{answer: UnpairAnswer{Done: true, Answered: true}}
			var input io.Reader = strings.NewReader(tc.answer)
			if tc.name == "end of input" {
				input = strings.NewReader("")
			}
			env, stdout, stderr := unpairCommandEnv(t, input, recorder)
			code := Run(context.Background(), []string{"unpair"}, env, nil)
			if tc.continueRun {
				want := "This removes this computer's pairing with http://localhost:8080.\ntervi will stop running in the background. Continue? [y/N]\n✓ Unpaired. This computer is no longer paired with http://localhost:8080.\n"
				if code != 0 || stdout.String() != want || stderr.Len() != 0 {
					t.Errorf("Run(unpair) = %d, stdout=%q stderr=%q; want %q", code, stdout.String(), stderr.String(), want)
				}
				if recorder.tellCalls != 1 || recorder.removeCalls != 1 {
					t.Errorf("confirmed unpair calls = (server %d, remove %d), want (1, 1)", recorder.tellCalls, recorder.removeCalls)
				}
			} else {
				want := "This removes this computer's pairing with http://localhost:8080.\ntervi will stop running in the background. Continue? [y/N]\nNothing was changed.\n"
				if code != 1 || stdout.String() != want || stderr.Len() != 0 {
					t.Errorf("Run(unpair) = %d, stdout=%q stderr=%q; want %q", code, stdout.String(), stderr.String(), want)
				}
				if recorder.tellCalls != 0 || recorder.removeCalls != 0 {
					t.Errorf("cancelled unpair did work: server %d remove %d", recorder.tellCalls, recorder.removeCalls)
				}
			}
		})
	}
}

func TestUnpairYesSkipsQuestion(t *testing.T) {
	for _, args := range [][]string{{"unpair", "--yes"}, {"unpair", "--local-only", "--yes"}} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			recorder := &unpairRecorder{answer: UnpairAnswer{Done: true, Answered: true}}
			stdin := &trapReader{}
			env, stdout, stderr := unpairCommandEnv(t, stdin, recorder)
			code := Run(context.Background(), args, env, nil)
			if code != 0 || stderr.Len() != 0 {
				t.Errorf("Run(%v) = %d, stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String(), "Continue?") || !strings.Contains(stdout.String(), "✓ Unpaired") {
				t.Errorf("--yes did not skip question: %q", stdout.String())
			}
			if stdin.reads != 0 {
				t.Errorf("--yes read stdin %d times", stdin.reads)
			}
		})
	}
}

func TestUnpairLocalOnlyQuestion(t *testing.T) {
	recorder := &unpairRecorder{}
	env, stdout, stderr := unpairCommandEnv(t, strings.NewReader("YES\n"), recorder)
	code := Run(context.Background(), []string{"unpair", "--local-only"}, env, nil)
	want := "This removes this computer's pairing without telling http://localhost:8080.\nIf that server still exists, it will keep listing this computer until you revoke it on its Machines page. Continue? [y/N]\n✓ Unpaired on this computer only.\n"
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("Run(unpair --local-only) = %d, stdout=%q stderr=%q; want %q", code, stdout.String(), stderr.String(), want)
	}
	if recorder.tellCalls != 0 || recorder.removeCalls != 1 {
		t.Errorf("local-only calls = (server %d, remove %d), want (0, 1)", recorder.tellCalls, recorder.removeCalls)
	}
}

func TestUnpairServerAnswersAndFailures(t *testing.T) {
	cases := []struct {
		name   string
		answer UnpairAnswer
		want   string
		code   int
	}{
		{name: "no answer", answer: UnpairAnswer{}, want: "✗ No answer from http://localhost:8080.\n  The server may or may not have unpaired this computer. Nothing was removed here.\n  Run tervi unpair again to finish, or revoke this computer on the Machines page.\n", code: 1},
		{name: "server error", answer: UnpairAnswer{Answered: true, Status: 500}, want: "✗ http://localhost:8080 couldn't unpair this computer (HTTP 500). Nothing was removed here.\n  Run tervi unpair again to finish, or revoke this computer on the Machines page.\n", code: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &unpairRecorder{answer: tc.answer}
			env, stdout, stderr := unpairCommandEnv(t, strings.NewReader("y\n"), recorder)
			code := Run(context.Background(), []string{"unpair"}, env, nil)
			if code != tc.code || !strings.HasSuffix(stdout.String(), tc.want) || stderr.Len() != 0 {
				t.Errorf("Run(unpair) = %d, stdout=%q stderr=%q; want suffix %q", code, stdout.String(), stderr.String(), tc.want)
			}
			if recorder.removeCalls != 0 {
				t.Errorf("failed server answer removed local files %d times", recorder.removeCalls)
			}
		})
	}
}

func TestUnpairLingerLine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		linger   bool
		wantLine bool
	}{
		{name: "on", linger: true, wantLine: true},
		{name: "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &unpairRecorder{answer: UnpairAnswer{Done: true, Answered: true}, linger: tc.linger}
			env, stdout, _ := unpairCommandEnv(t, strings.NewReader("y\n"), recorder)
			code := Run(context.Background(), []string{"unpair"}, env, nil)
			gotLine := strings.Contains(stdout.String(), "  Linger is still on. Turn it off with: loginctl disable-linger\n")
			if code != 0 || gotLine != tc.wantLine || recorder.lingerCalls != 1 {
				t.Errorf("linger result code=%d line=%v calls=%d stdout=%q", code, gotLine, recorder.lingerCalls, stdout.String())
			}
		})
	}
}

func TestUnpairArgumentErrors(t *testing.T) {
	for _, args := range [][]string{{"unpair", "extra"}, {"unpair", "--unknown"}, {"unpair", "--yes", "--yes"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			recorder := &unpairRecorder{}
			env, stdout, stderr := unpairCommandEnv(t, strings.NewReader("y\n"), recorder)
			code := Run(context.Background(), args, env, nil)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage: tervi pair") {
				t.Errorf("Run(%v) = %d, stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
			}
			if recorder.tellCalls != 0 || recorder.removeCalls != 0 {
				t.Errorf("invalid arguments performed work: %+v", recorder)
			}
		})
	}
}
