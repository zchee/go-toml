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

//go:build force_swar || (!arm64 && (!amd64 || !goexperiment.simd))

package scan

import "testing"

// dispatch_swar_test.go is the SWAR row of AC-SIMD-7's dispatch
// enforcement. The build tag mirrors scan_swar.go's tag exactly so this
// file is compiled in every configuration where the *SWAR symbols are
// available — both the vanilla SWAR-only paths and the `force_swar`
// verification path that AC-SIMD-7 mandates at plan line 1056.
//
// The test does NOT call t.Parallel(); it swaps package-level dispatch
// vars and t.Cleanup restores them.

// TestDispatch_SWAR pins every dispatch var to its SWAR variant and
// re-runs the shared smoke fixture through the exported API. This is
// the SWAR row of the AC-SIMD-7 audit matrix.
func TestDispatch_SWAR(t *testing.T) {
	pinAllDispatch(
		t,
		scanBareKeySWAR,
		scanBasicStringSWAR,
		scanBasicStringEscapeSWAR,
		scanBasicStringStrictSWAR,
		scanCommentBodySWAR,
		scanBareValueEndSWAR,
		countLinesSWAR,
		scanLiteralStringSWAR,
		skipWhitespaceSWAR,
		locateNewlineSWAR,
		validateUTF8SWAR,
	)
	runDispatchSmoke(t)
}
