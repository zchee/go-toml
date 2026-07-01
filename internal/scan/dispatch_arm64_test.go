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

//go:build !force_swar && arm64

package scan

import "testing"

// dispatch_arm64_test.go is the arm64 half of AC-SIMD-7's dispatch
// enforcement. NEON is ABI-guaranteed on arm64 so there is only one
// SIMD variant and no host feature gate is needed — the test pins each
// dispatch var to the NEON variant and re-runs the shared smoke fixture
// through the exported API.
//
// The test does NOT call t.Parallel(); it swaps package-level dispatch
// vars and t.Cleanup restores them.

// TestDispatch_ARM64_NEON pins every dispatch var to its NEON variant
// and re-runs the shared smoke fixture through the exported API. This
// is the NEON row of the AC-SIMD-7 audit matrix on arm64.
func TestDispatch_ARM64_NEON(t *testing.T) {
	pinAllDispatch(
		t,
		scanBareKeyNEON,
		scanBasicStringNEON,
		scanBasicStringStrictNEON,
		scanCommentBodyNEON,
		scanBareValueEndNEON,
		countLinesNEON,
		scanLiteralStringNEON,
		skipWhitespaceNEON,
		locateNewlineNEON,
		validateUTF8NEON,
	)
	runDispatchSmoke(t)
}
