package main

import (
	"context"
	"errors"
	"strings"
)

// Lifecycle contract for an in-memory simulation. No SSH adapter,
// real commands, paths, credentials, or runtime wiring are provided.
// Production remains inventory-only. A successful simulation is not
// evidence that the remote service was installed.
type lifecycleAction string

const (
	lifecycleInstall lifecycleAction = "install"
	lifecycleUpdate  lifecycleAction = "update"
	lifecycleRemove  lifecycleAction = "uninstall"
)

type lifecycleStage string

const (
	lifecycleChecked   lifecycleStage = "checked"
	lifecycleStaged    lifecycleStage = "staged"
	lifecycleExecuted  lifecycleStage = "executed"
	lifecycleConfirmed lifecycleStage = "confirmed"
)

type lifecycleReceipt struct {
	exit   int
	output string
}

type lifecycleSimulation interface {
	check(context.Context, lifecycleAction) error
	stage(context.Context, lifecycleAction) error
	execute(context.Context, lifecycleAction) (lifecycleReceipt, error)
	postcheck(context.Context, lifecycleAction, string) error
}

type lifecycleOutcome struct {
	stages []lifecycleStage
	result string
}

func lifecycleReceiptResult(action lifecycleAction, r lifecycleReceipt) string {
	if len(r.output) > 65536 {
		return "unknown"
	}
	if action != lifecycleInstall && action != lifecycleUpdate && action != lifecycleRemove {
		return "unknown"
	}
	var install, remove, restored, rollbackFailed, reportedError int
	for _, raw := range strings.Split(r.output, "\n") {
		line := strings.TrimSpace(raw)
		switch line {
		case "CSQTT_DEPLOY_OK", "CSQTT_CORE_DEPLOY_OK":
			install++
		case "CSQTT_UNINSTALL_OK", "CSQTT_CORE_UNINSTALL_OK":
			remove++
		case "CSQTT_DEPLOY_ROLLED_BACK":
			restored++
		case "CSQTT_DEPLOY_ROLLBACK_FAILED":
			rollbackFailed++
		}
		if strings.HasPrefix(line, "CSQTT_DEPLOY_ERROR|") {
			reportedError++
		}
	}
	if r.exit == 0 {
		if reportedError+restored+rollbackFailed != 0 {
			return "unknown"
		}
		if action == lifecycleRemove && remove == 1 && install == 0 {
			return "uninstalled"
		}
		if action != lifecycleRemove && install == 1 && remove == 0 {
			return "installed"
		}
		return "unknown"
	}
	// 2.5.3 recognizes exit 30/31; require matching marker as well,
	// and do not infer rollback from an uninstall error.
	if action != lifecycleRemove && install+remove == 0 {
		if r.exit == 31 && rollbackFailed == 1 && restored == 0 {
			return "rollback_failed"
		}
		if r.exit == 30 && restored == 1 && rollbackFailed == 0 {
			return "restored"
		}
	}
	return "error"
}

func simulateDeployLifecycle(ctx context.Context, a lifecycleAction, s lifecycleSimulation) (lifecycleOutcome, error) {
	out := lifecycleOutcome{result: "unknown"}
	if a != lifecycleInstall && a != lifecycleUpdate && a != lifecycleRemove {
		return out, errors.New("unsupported lifecycle action")
	}
	if s == nil {
		return out, errors.New("no simulation")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := s.check(ctx, a); err != nil {
		return out, errors.New("preflight failed")
	}
	out.stages = append(out.stages, lifecycleChecked)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	// Original performUninstall also uploads the installer script.
	// In this simulation all modes have a staging step; uninstall
	// has no server binary or new credentials. No actual upload or
	// installer execution is implemented by this contract.
	if err := s.stage(ctx, a); err != nil {
		return out, errors.New("staging failed")
	}
	out.stages = append(out.stages, lifecycleStaged)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	// Exactly one execute attempt. A lost connection is an unknown
	// outcome; no automatic retry of an operation with side effects.
	r, err := s.execute(ctx, a)
	if err != nil {
		return out, errors.New("execution outcome unknown")
	}
	out.stages = append(out.stages, lifecycleExecuted)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	result := lifecycleReceiptResult(a, r)
	if result == "rollback_failed" || result == "error" || result == "unknown" {
		out.result = result
		return out, errors.New("operation not confirmed")
	}
	if err := s.postcheck(ctx, a, result); err != nil {
		return out, errors.New("independent postcheck failed")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.stages = append(out.stages, lifecycleConfirmed)
	out.result = result
	if result == "restored" {
		return out, errors.New("installation failed; previous state restored")
	}
	return out, nil
}
