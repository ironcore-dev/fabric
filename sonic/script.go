// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ironcore-dev/fabric/cellruntime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ProviderName = "sonic"
	doneFlagFile = "/etc/sonic/fabric-cell.done"
	scriptDir    = "/etc/sonic"
)

//go:embed templates
var templates embed.FS

type ScriptRuntime struct {
	role         string
	region       string
	ipv6Base     string
	searchDomain string
}

var _ cellruntime.Runtime = (*ScriptRuntime)(nil)

func NewScriptRuntime(role, region, ipv6Base, searchDomain string) (*ScriptRuntime, error) {
	if _, err := templates.ReadFile("templates/" + role + ".sh"); err != nil {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	for name, value := range map[string]string{
		"region":       region,
		"ipv6Base":     ipv6Base,
		"searchDomain": searchDomain,
	} {
		if value == "" {
			return nil, fmt.Errorf("must specify %s", name)
		}
	}
	return &ScriptRuntime{
		role:         role,
		region:       region,
		ipv6Base:     ipv6Base,
		searchDomain: searchDomain,
	}, nil
}

func (r *ScriptRuntime) ProviderName() string {
	return ProviderName
}

func (r *ScriptRuntime) NodeID(ctx context.Context, node string) (string, error) {
	return node, nil
}

func (r *ScriptRuntime) ApplyCell(ctx context.Context, node string, cfg *cellruntime.CellConfig) error {
	script, err := r.renderCell(cfg)
	if err != nil {
		return err
	}
	return runScript(ctx, script)
}

// TODO: factory-reset the switch here. Runs the (currently noop) reset script.
func (r *ScriptRuntime) DeleteCell(ctx context.Context, node string) error {
	script, err := r.renderReset(node)
	if err != nil {
		return err
	}
	return runScript(ctx, script)
}

func (r *ScriptRuntime) renderCell(cfg *cellruntime.CellConfig) ([]byte, error) {
	if _, err := strconv.Atoi(cfg.ID); err != nil {
		return nil, cellruntime.TerminalError(fmt.Errorf("cell ID %q must be numeric: %w", cfg.ID, err))
	}
	return render(r.role+".sh",
		"__id__", cfg.ID,
		"__region__", r.region,
		"__ipv6_base__", r.ipv6Base,
		"__search_domain__", r.searchDomain,
	)
}

func (r *ScriptRuntime) renderReset(node string) ([]byte, error) {
	return render("reset.sh", "__node__", node)
}

func (r *ScriptRuntime) CellStatus(ctx context.Context, node string) (*cellruntime.CellStatus, error) {
	if !fileExists(doneFlagFile) {
		return nil, fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}
	return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseActive}, nil
}

func (r *ScriptRuntime) InterfaceID(ctx context.Context, iface string) (string, error) {
	return iface, nil
}

func (r *ScriptRuntime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	operstate, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "operstate"))
	if err != nil {
		return nil, fmt.Errorf("reading operstate of interface %s: %w", iface, err)
	}
	return &cellruntime.InterfaceState{
		Up: strings.TrimSpace(string(operstate)) == "up",
	}, nil
}

func (r *ScriptRuntime) SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error {
	return nil
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

func render(templateName string, values ...string) ([]byte, error) {
	template, err := templates.ReadFile("templates/" + templateName)
	if err != nil {
		return nil, cellruntime.TerminalError(fmt.Errorf("reading ZTP template: %w", err))
	}
	return []byte(strings.NewReplacer(values...).Replace(string(template))), nil
}

func runScript(ctx context.Context, script []byte) error {
	f, err := os.CreateTemp(scriptDir, "fabric-ztp-*.sh")
	if err != nil {
		return fmt.Errorf("creating ZTP script file: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	if _, err := f.Write(script); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing ZTP script %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing ZTP script %s: %w", f.Name(), err)
	}

	out, err := exec.CommandContext(ctx, "bash", f.Name()).CombinedOutput()
	if err != nil {
		return cellruntime.TerminalError(fmt.Errorf("running ZTP script: %w: %s", err, strings.TrimSpace(string(out))))
	}

	log.FromContext(ctx).V(1).Info("ZTP script completed", "output", strings.TrimSpace(string(out)))
	return nil
}
