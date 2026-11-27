package main

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func Test253ExecResultStreaming(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// The child waits for input after emitting progress. This proves the
	// callback runs before process exit, not just from a buffered transcript.
	cmd := exec.CommandContext(ctx, "sh", "-c", "printf 'CSQTT_PROGRESS|0.25|stage\\n'; read ack; printf 'tail'; printf 'diagnostic\\n' >&2; exit 20")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostic []string
	var mu sync.Mutex
	r, err := execResult(ctx, cmd, func(s string) {
		mu.Lock()
		out = append(out, s)
		mu.Unlock()
		if strings.HasPrefix(s, "CSQTT_PROGRESS|") {
			_, _ = input.Write([]byte("ok\n"))
			_ = input.Close()
		}
	}, func(s string) { mu.Lock(); diagnostic = append(diagnostic, s); mu.Unlock() })
	if err != nil || r.exitStatus != 20 || r.stdout != "CSQTT_PROGRESS|0.25|stage\ntail" || r.stderr != "diagnostic\n" {
		t.Fatalf("unexpected result: %+v, %v", r, err)
	}
	if len(out) != 2 || out[1] != "tail" || len(diagnostic) != 1 {
		t.Fatal("streams were not delivered separately")
	}
}

func Test253ExecResultUnknown(t *testing.T) {
	for _, code := range []string{"255", "0"} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		script := "exit " + code
		if code == "0" {
			script = "sleep 1"
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", script)
		cmd.WaitDelay = 50 * time.Millisecond
		r, err := execResult(ctx, cmd, nil, nil)
		cancel()
		if err == nil || r.exitStatus != -1 {
			t.Fatal("unknown result accepted")
		}
	}
}
