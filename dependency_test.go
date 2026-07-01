// Copyright 2026 The go-toml Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package toml

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompetitorDependenciesAreBenchOnly(t *testing.T) {
	t.Parallel()

	repoRoot := testRepoRoot(t)
	tests := map[string]struct {
		dir          string
		args         []string
		wantAbsent   []string
		wantContains []string
	}{
		"success: production graph excludes competitors": {
			dir:        repoRoot,
			args:       []string{"list", "-deps", "./..."},
			wantAbsent: []string{"github.com/BurntSushi/toml", "github.com/pelletier/go-toml/v2"},
		},
		"success: ordinary test graph excludes competitors": {
			dir:        repoRoot,
			args:       []string{"list", "-deps", "-test", "./..."},
			wantAbsent: []string{"github.com/BurntSushi/toml", "github.com/pelletier/go-toml/v2"},
		},
		"success: benchmark submodule owns competitors": {
			dir:  filepath.Join(repoRoot, "benchmark"),
			args: []string{"list", "-deps", "-test", "."},
			wantContains: []string{
				"github.com/BurntSushi/toml",
				"github.com/pelletier/go-toml/v2",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := runGoList(t, tc.dir, tc.args...)
			for _, needle := range tc.wantAbsent {
				if strings.Contains(out, needle) {
					t.Fatalf("go %s unexpectedly contains %q\n%s", strings.Join(tc.args, " "), needle, out)
				}
			}
			lines := dependencyLines(out)
			for _, needle := range tc.wantContains {
				if !lines[needle] {
					t.Fatalf("go %s missing dependency %q\n%s", strings.Join(tc.args, " "), needle, out)
				}
			}
		})
	}
}

func testRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root not found from %s", dir)
		}
		dir = parent
	}
}

func runGoList(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func dependencyLines(out string) map[string]bool {
	lines := make(map[string]bool)
	for line := range strings.Lines(out) {
		lines[strings.TrimSpace(line)] = true
	}
	return lines
}
