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

//go:build force_swar

// Package scan, force_swar variant — see scan_swar.go for the actual
// SWAR backend implementation.
//
// The `-tags=force_swar` build mode is the verification path that
// AC-SIMD-7 requires: it forces the SWAR kernels to be the dispatched
// implementation on every arch (including amd64 with AVX2/SSE2 and arm64
// with NEON) so that the same correctness fixtures can be re-run against
// SWAR for cross-backend equivalence.
//
// # How force_swar selects the SWAR backend
//
// This package's build-tag layout is intentionally minimal — no central
// dispatch.go, no init-order tricks. Each backend file gates itself with
// a mutually exclusive build tag so that on any (arch, GOEXPERIMENT,
// force_swar) combination exactly one file declares the dispatch vars:
//
//	scan_amd64.go : !force_swar && goexperiment.simd && amd64
//	scan_arm64.go : !force_swar && arm64
//	scan_swar.go  : force_swar || (!arm64 && (!amd64 || !goexperiment.simd))
//
// When force_swar is set, the leading `!force_swar &&` clauses on the
// two arch backends exclude them from the build, and the leading
// `force_swar ||` clause on scan_swar.go pulls it in regardless of arch.
// scan_swar.go's own var block then provides the dispatch bindings.
//
// This file is intentionally body-only-comment: it documents the design
// for readers who look for a file named "scan_force_swar.go" but does
// not itself contribute symbols. Splitting documentation out of
// scan_swar.go's header keeps the SWAR implementation file focused on
// its kernels.

package scan
