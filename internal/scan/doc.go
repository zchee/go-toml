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

// Package scan provides the ten SIMD-accelerated byte-class scan kernels
// consumed by the streaming decoder hot loop in toml.
//
// The kernels are ScanBareKey, ScanBasicString, ScanBasicStringStrict,
// ScanCommentBody, ScanBareValueEnd, CountLines, ScanLiteralString,
// SkipWhitespace, LocateNewline, and ValidateUTF8. Each takes a single
// []byte haystack and returns an int: a count-or-index that the decoder
// composes into token boundaries or lazy line/column positions. None of
// the kernels return an
// error; the typed LimitError lives in this package only because it is the
// shape every consumer of these kernels uses when it enforces a DoS cap on
// top of a scan result.
//
// # Build-tag matrix
//
// The package selects an implementation per (GOARCH, goexperiment.simd)
// combination:
//
//   - amd64 && goexperiment.simd:        AVX2 (when archsimd.X86.AVX2())
//     else SSE2; both via simd/archsimd intrinsics. (Phase 1 / T2.)
//   - amd64 && !goexperiment.simd:       SWAR fallback. (Phase 1 / T1 + T4.)
//   - arm64:                             Hand-rolled NEON Plan 9 assembly,
//     no runtime feature detect. (Phase 1 / T3.)
//   - all other arches (386, ppc64le, riscv64, wasm, ...): SWAR. (Phase 1
//     / T1.)
//
// The build tags are spelled on each scan_*.go file; the SWAR file uses
// "!goexperiment.simd || (!amd64 && !arm64)" so it is the union of the
// non-SIMD platforms.
//
// # Maintenance contract
//
// This package intentionally keeps parser semantics out of backend files, but
// TOML grammar changes that alter a scan class must update all of these
// surfaces together:
//
//   - api.go for the public internal wrapper contract.
//   - strict.go for the scalar oracle predicate or tail helper.
//   - scan_swar.go for the SWAR fallback.
//   - scan_amd64.go for the amd64 SIMD path.
//   - scan_arm64.go and scan_arm64.s for the arm64 NEON path.
//   - naive_scan_test.go, scan_test.go, property_test.go, and fuzz_test.go for
//     oracle, golden, property, and fuzz parity.
//   - bench_test.go when the changed class has a throughput gate.
//
// Keep the table in this file, README.md, and the CI workflow in sync with any
// new or retired scan kernel. Do not change only one backend for a grammar
// tweak unless the other backends are covered by explicit tests proving they do
// not need the update.
//
// # Lifetime and aliasing rules
//
// Each kernel reads from its haystack and returns immediately; it retains
// no reference to the slice across the call boundary. The kernels do not
// allocate, do not call into the parser layer, do not call into runtime
// stack-walking primitives, and never escape the haystack pointer outside
// the package. Callers may reuse, mutate, or free the slice as soon as the
// kernel returns. The kernels use unsafe.Pointer internally for unaligned
// 8-byte loads on the SWAR path and for archsimd vector loads on amd64;
// the use is "go vet -unsafeptr" clean and does not survive across the
// call boundary (AC-SIMD-8).
//
// # SWAR endianness invariant (Critic CC7)
//
// The SWAR fallback uses 64-bit word loads via unsafe.Pointer with the
// classic hasZeroByte trick. The detection step is endian-neutral, but
// the index-extraction step uses math/bits.TrailingZeros64, which assumes
// little-endian byte ordering in a 64-bit word. All currently supported
// GOARCHes (amd64, arm64, 386, ppc64le, riscv64, wasm) are little-endian
// or word-position-agnostic. Adding a big-endian GOARCH (mips, mips64,
// ppc64) requires either a tested big-endian SWAR variant OR an explicit
// build-tag refusal of this package on that arch.
//
// # AC-SIMD-5 baseline table (Critic mandatory fix #13)
//
// Each scan kernel, on a 64 KB buffer, must meet its declared gate ratio on
// amd64 (with goexperiment.simd) and arm64. The class and UTF-8 scans use the
// Cross-cutting Bench protocol's strict 1.0 lower-95%-confidence threshold;
// the stdlib-backed single-byte scans use a 0.98 non-regression threshold so
// like-for-like bytes.IndexByte wrapper/toolchain noise does not masquerade as
// a meaningful SIMD signal. The naive-loop reference for class scans is the
// same code as the correctness oracle in naive_scan_test.go — one source of
// truth for both baseline-perf and SIMD-correctness checks.
//
//	+-------------------+----------------------------------------+------------+-----------------------------------------------------------+
//	| Scan              | Baseline                               | Gate ratio | Why this baseline                                         |
//	+-------------------+----------------------------------------+------------+-----------------------------------------------------------+
//	| LocateNewline     | bytes.IndexByte(s, '\n')               | 0.98       | True single-byte; like-for-like comparison.               |
//	| ScanLiteralString | bytes.IndexByte(s, '\'')               | 0.98       | True single-byte; like-for-like comparison.               |
//	| ScanBasicString   | naive Go loop (naive_scan_test.go)     | 1.00       | Two-byte class ('"' or '\\'); IndexByte is not a fair    |
//	|                   |                                        |            | comparator.                                               |
//	| ScanBasicStringStrict| naive Go loop (naive_scan_test.go) | 1.00       | Fused delimiter/control class with TOML tab exception.    |
//	| ScanCommentBody  | naive Go loop (naive_scan_test.go)     | 1.00       | Comment LF/CR/control stop class with tab exception.      |
//	| ScanBareValueEnd | naive Go loop (naive_scan_test.go)     | 1.00       | Bare-value delimiter byte class.                          |
//	| CountLines       | naive Go loop (naive_scan_test.go)     | 1.00       | LF counting for lazy diagnostics.                         |
//	| ScanBareKey      | naive Go loop (naive_scan_test.go)     | 1.00       | Multi-byte class predicate [A-Za-z0-9_-].                 |
//	| SkipWhitespace    | naive Go loop (naive_scan_test.go)     | 1.00       | Two-byte class predicate (' ' or '\t', excluding '\n').   |
//	| ValidateUTF8      | unicode/utf8.Valid                     | 1.00       | Multi-state class validator; SIMD must beat stdlib       |
//	|                   |                                        |            | utf8.Valid, not just a naive byte loop.                   |
//	+-------------------+----------------------------------------+------------+-----------------------------------------------------------+
//
// The baseline applies at 64 KB buffer size on the hot path
// (BenchmarkScanX/n=64K); smaller sizes (n=16, 64, 256, 1K, 4K) are
// tracked informationally to catch tail-overhead regressions.
//
// Exception clause: if a scan fails its gate on a given arch, the
// dispatcher binds the SWAR variant for that scan on that arch and the
// failure is documented in this file under "Documented exceptions" below.
//
// # Documented exceptions
//
// (none yet — populated by Phase 1 / T5 when a SIMD variant misses its
// gate and dispatch is bound to SWAR for that scan on that arch.)
package scan
