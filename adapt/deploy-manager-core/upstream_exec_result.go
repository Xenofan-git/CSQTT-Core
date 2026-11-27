package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
)

// Adapt CSQTTR 2.5.3 DeploySSHClient.execResult: drain both streams while
// running, wait for a confirmed exit, and close the owned SSH process on
// cancellation. Command selection remains with the operation-specific caller.
type readOnlyOperation uint8

const (
	readOnlyMachine readOnlyOperation = iota + 1
	readOnlyInventory
)

type readOnlyExecResult struct {
	exitStatus int
	stdout     string
	stderr     string
}

// execResult never retries a command: loss of SSH after a mutation is an
// unknown outcome. Only stdout can supply operation success evidence.
// Output callbacks run serially for each stream and must not block.
func execResult(ctx context.Context, cmd *exec.Cmd, stdoutLine func(string), stderrLine func(string)) (readOnlyExecResult, error) {
	unknown := readOnlyExecResult{exitStatus: -1}
	if err := ctx.Err(); err != nil {
		return unknown, err
	}
	if cmd == nil {
		return unknown, errors.New("missing SSH command")
	}
	var stdout, stderr execOutput
	stdout.onLine, stderr.onLine = stdoutLine, stderrLine
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return unknown, ctx.Err()
	}
	if stdout.truncated || stderr.truncated {
		return unknown, errors.New("SSH output limit exceeded")
	}
	// Surface a final unterminated line only after stream copies have stopped.
	stdout.finish()
	stderr.finish()
	status := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return unknown, errors.New("SSH process did not complete")
		}
		status = exitErr.ExitCode()
		if status < 0 || status == 255 {
			return unknown, errors.New("SSH exit status unknown")
		}
	}
	return readOnlyExecResult{exitStatus: status, stdout: stdout.String(), stderr: stderr.String()}, nil
}

// Do not embed bytes.Buffer: io.Copy must use the bounded Write method.
// Retain the complete bounded transcript, including progress, for validation.
// Callback input is bounded by the same budget; callers redact before display.
type execOutput struct {
	boundedInventoryBuffer
	pending string
	onLine  func(string)
}

func (o *execOutput) Write(data []byte) (int, error) {
	n, err := o.boundedInventoryBuffer.Write(data)
	if err != nil {
		return n, err
	}
	if o.onLine != nil {
		o.pending += string(data)
		for {
			line, tail, ok := strings.Cut(o.pending, "\n")
			if !ok {
				break
			}
			o.pending = tail
			o.onLine(strings.TrimSuffix(line, "\r"))
		}
	}
	return n, nil
}
func (o *execOutput) finish() {
	if o.onLine != nil && o.pending != "" {
		o.onLine(strings.TrimSuffix(o.pending, "\r"))
		o.pending = ""
	}
}

func (t *remoteTransport) execReadOnlyResult(ctx context.Context, operation readOnlyOperation, script []byte) (readOnlyExecResult, error) {
	unknown := readOnlyExecResult{exitStatus: -1}
	if err := ctx.Err(); err != nil {
		return unknown, err
	}
	var command string
	var input io.Reader
	switch operation {
	case readOnlyMachine:
		if len(script) != 0 {
			return unknown, errors.New("unexpected architecture input")
		}
		command = "uname -m"
	case readOnlyInventory:
		command = "bash -s inventory"
		input = bytes.NewReader(script)
	default:
		return unknown, errors.New("unsupported read-only operation")
	}
	cmd := t.command(ctx, false, command)
	cmd.Stdin = input
	return execResult(ctx, cmd, nil, nil)
}
