// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ironcore-dev/fabric/cellruntime"
)

var update = flag.Bool("update", false, "update golden files")

// compareGolden compares got with the golden file testdata/<name>.
// Regenerate with: go test ./sonic/ -update
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with -update to create it)", path, err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("rendered script differs from %s (-want +got):\n%s", path, diff)
	}
}

func testRuntime(t *testing.T, role string) *ScriptRuntime {
	t.Helper()

	rt, err := NewScriptRuntime(role, "ab", "fd00:1234:5678", "fabric.example.com")
	if err != nil {
		t.Fatalf("creating runtime: %v", err)
	}
	return rt
}

var placeholders = []string{"__id__", "__region__", "__ipv6_base__", "__search_domain__", "__node__"}

func TestRenderCell(t *testing.T) {
	for _, role := range []string{"inband-leaf", "inband-spine"} {
		t.Run(role, func(t *testing.T) {
			got, err := testRuntime(t, role).renderCell(&cellruntime.CellConfig{ID: "42"})
			if err != nil {
				t.Fatalf("rendering cell: %v", err)
			}

			for _, placeholder := range placeholders {
				if strings.Contains(string(got), placeholder) {
					t.Errorf("rendered script still contains %s", placeholder)
				}
			}
			compareGolden(t, role+".golden.sh", got)
		})
	}
}

func TestRenderReset(t *testing.T) {
	got, err := testRuntime(t, "inband-leaf").renderReset("swi1-ab-42")
	if err != nil {
		t.Fatalf("rendering reset: %v", err)
	}
	compareGolden(t, "reset.golden.sh", got)
}

func TestRenderCellNonNumericID(t *testing.T) {
	_, err := testRuntime(t, "inband-leaf").renderCell(&cellruntime.CellConfig{ID: "abc"})
	if !errors.Is(err, cellruntime.TerminalError(nil)) {
		t.Errorf("expected terminal error for non-numeric cell ID, got %v", err)
	}
}

func TestNewScriptRuntimeValidatesRole(t *testing.T) {
	if _, err := NewScriptRuntime("oob-leaf", "ab", "fd00:1234:5678", "fabric.example.com"); err == nil {
		t.Error("expected error for role without template")
	}
	if _, err := NewScriptRuntime("inband-leaf", "", "fd00:1234:5678", "fabric.example.com"); err == nil {
		t.Error("expected error for empty region")
	}
}
