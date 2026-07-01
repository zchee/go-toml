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

package toml_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	wantGoModDirective = "go 1.27"
	wantCargoLockSHA   = "9ea94b60b3ee80c73f52186946bb280dc41c7287bbb678988618a6839533dbe9"
	wantCargoLockBytes = 103263
)

func TestUpstreamPinsGoModDirective(t *testing.T) {
	t.Parallel()

	body := mustReadRepoFile(t, "go.mod")
	if !strings.Contains(body, wantGoModDirective+"\n") {
		t.Fatalf("go.mod missing directive %q\n--- go.mod ---\n%s", wantGoModDirective, body)
	}
}

func TestUpstreamPinsCargoLockCorpus(t *testing.T) {
	t.Parallel()

	path := mustRepoPath(t, "testdata/corpus/cargo.lock")
	body := mustReadRepoFile(t, "testdata/corpus/cargo.lock")

	sum := sha256.Sum256([]byte(body))
	if got := hex.EncodeToString(sum[:]); got != wantCargoLockSHA {
		t.Fatalf("%s sha256 = %s, want %s", path, got, wantCargoLockSHA)
	}
	if got := len(body); got != wantCargoLockBytes {
		t.Fatalf("%s byte length = %d, want %d", path, got, wantCargoLockBytes)
	}
}

func mustReadRepoFile(t *testing.T, rel string) string {
	t.Helper()

	path := mustRepoPath(t, rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}
	return string(body)
}

func mustRepoPath(t *testing.T, rel string) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	repoRoot := filepath.Dir(file)
	return filepath.Join(repoRoot, filepath.FromSlash(rel))
}
