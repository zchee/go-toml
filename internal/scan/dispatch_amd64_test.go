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

//go:build !force_swar && goexperiment.simd && amd64

package scan

import (
	"simd/archsimd"
	"testing"
)

// dispatch_amd64_test.go is the amd64 half of AC-SIMD-7's cross-variant
// dispatch enforcement. The build tag matches scan_amd64.go exactly so
// the SSE2/AVX2 symbols this file references are guaranteed to be
// compiled in. The AVX2 case skips on hosts without AVX2 instead of
// failing — the SSE2 case always runs because the amd64 Go ABI
// guarantees SSE2 availability.
//
// Both tests deliberately do NOT call t.Parallel(): they swap
// package-level dispatch vars, and parallel siblings would race on the
// same memory. t.Cleanup restores the original bindings so subsequent
// tests in the package see the dispatcher init() chose.

// TestDispatch_AMD64_SSE2 pins every dispatch var to its SSE2 variant
// and re-runs the shared smoke fixture through the exported API.
// This is the SSE2 row of the AC-SIMD-7 audit matrix on amd64.
func TestDispatch_AMD64_SSE2(t *testing.T) {
	pinAllDispatch(
		t,
		scanBareKeySSE2,
		scanBasicStringSSE2,
		scanBasicStringStrictSSE2,
		scanCommentBodySSE2,
		scanBareValueEndSSE2,
		countLinesSSE2,
		scanLiteralStringSSE2,
		skipWhitespaceSSE2,
		locateNewlineSSE2,
		validateUTF8SSE2,
	)
	runDispatchSmoke(t)
}

// TestDispatch_AMD64_AVX2 pins every dispatch var to its AVX2 variant
// and re-runs the shared smoke fixture. Hosts without AVX2 skip; CI
// covers the AVX2 path on ubuntu-latest where AVX2 is universally
// available on the public runners.
func TestDispatch_AMD64_AVX2(t *testing.T) {
	if !archsimd.X86.AVX2() {
		t.Skip("host lacks AVX2 — TestDispatch_AMD64_AVX2 requires it")
	}
	pinAllDispatch(
		t,
		scanBareKeyAVX2,
		scanBasicStringAVX2,
		scanBasicStringStrictAVX2,
		scanCommentBodyAVX2,
		scanBareValueEndAVX2,
		countLinesAVX2,
		scanLiteralStringAVX2,
		skipWhitespaceAVX2,
		locateNewlineAVX2,
		validateUTF8AVX2,
	)
	runDispatchSmoke(t)
}
