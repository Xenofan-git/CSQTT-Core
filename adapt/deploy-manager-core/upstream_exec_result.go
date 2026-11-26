package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// Adapt the result boundary of CSQTTR 2.5.3 DeploySSHClient.execResult
// for the existing read-only transport. OpenSSH owns authentication;
// os/exec drains stdout and stderr independently and waits for exit.
// Only the two existing read-only operations are accepted here. No
// arbitrary command, installer action, progress or lifecycle is exposed.
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

func (t *remoteTransport) execReadOnlyResult(ctx context.Context, operation readOnlyOperation, script []byte) (readOnlyExecResult, error) {
	unknown := readOnlyExecResult{exitStatus: -1}
	if err := ctx.Err(); err != nil {
		return unknown, err
	}
	var command string
	switch operation {
	case readOnlyMachine:
		if len(script) != 0 {
			return unknown, errors.New("unexpected architecture input")
		}
		command = "uname -m"
	case readOnlyInventory:
		command = "bash -s inventory"
	default:
		return unknown, errors.New("unsupported read-only operation")
	}
	cmd := t.command(ctx, false, command)
	if operation == readOnlyInventory {
		cmd.Stdin = bytes.NewReader(script)
	}
	var stdout, stderr boundedInventoryBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return unknown, ctx.Err()
	}
	if stdout.truncated || stderr.truncated {
		return unknown, errors.New("SSH output limit exceeded")
	}
	status := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return unknown, errors.New("SSH process did not complete")
		}
		status = exitErr.ExitCode()
		// OpenSSH 255 denotes SSH failure or an indistinguishable remote
		// exit 255. A signal also has no confirmed remote exit status.
		if status < 0 || status == 255 {
			return unknown, errors.New("SSH exit status unknown")
		}
	}
	// Do not publish raw diagnostic streams; callers validate stdout.
	// In particular a marker on stderr cannot count as inventory evidence.
	return readOnlyExecResult{exitStatus: status, stdout: stdout.String(), stderr: stderr.String()}, nil
}
