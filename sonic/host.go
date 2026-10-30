// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type systemHostOperations struct{}

func (h *systemHostOperations) SaveConfig(ctx context.Context) error {
	return runHostCommand(ctx, "saving SONiC configuration", "config", "save", "-y")
}

func (h *systemHostOperations) CellApplied(ctx context.Context) (bool, error) {
	cmd := hostCommand(ctx, "test", "-f", doneFlagFile)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, commandError("checking cell marker", out, err)
}

func (h *systemHostOperations) MarkCellApplied(ctx context.Context) error {
	return runHostCommand(ctx, "creating cell marker", "touch", doneFlagFile)
}

func (h *systemHostOperations) ClearCellApplied(ctx context.Context) error {
	return runHostCommand(ctx, "removing cell marker", "rm", "-f", doneFlagFile)
}

func (h *systemHostOperations) Reboot(ctx context.Context) error {
	return runHostCommand(ctx, "rebooting switch", "reboot")
}

func hostCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	nsenterArgs := make([]string, 0, 8+len(args))
	nsenterArgs = append(nsenterArgs, "--target", "1", "--mount", "--net", "--pid", "--uts", "--", name)
	nsenterArgs = append(nsenterArgs, args...)
	return exec.CommandContext(ctx, "nsenter", nsenterArgs...)
}

func runHostCommand(ctx context.Context, action, name string, args ...string) error {
	out, err := hostCommand(ctx, name, args...).CombinedOutput()
	if err != nil {
		return commandError(action, out, err)
	}
	return nil
}

func commandError(action string, out []byte, err error) error {
	output := strings.TrimSpace(string(out))
	if output == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, output)
}
