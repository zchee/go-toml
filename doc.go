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

// Package toml provides a flat TOML API backed by a shared streaming parser.
//
// The low-level API starts with Decoder and Token for callers that need to
// inspect TOML input without building Go values. Tokens report byte Offset;
// deprecated Line and Col fields remain populated for source compatibility.
// New callers that need line/column display should derive it from the original
// source when formatting diagnostics. The high-level facade exposes Marshal
// and Unmarshal for struct, map, scalar, array, and datetime values.
// ParseDocument returns a format-preserving Document for edit-in-place workflows
// where comments, whitespace, and untouched source spans must remain
// byte-identical.
//
// Direct struct decoding binds escape-free strings from one immutable
// per-document string arena by default. This minimizes allocations without
// aliasing caller-owned input bytes. Use WithCopiedStrings when decoded values
// must not retain a document-sized arena.
//
// Benchmark-only comparisons against external TOML libraries live in the
// sibling benchmark module. Production builds of this package must not import
// those competitors; verify with:
//
//	go list -deps ./...
//
// Use the benchmark submodule when checking that the benchmark comparators are
// wired:
//
//	(cd benchmark && go list -mod=mod -deps -test .)
//
// The force_swar build tag selects the pure-Go scan backend for verification
// of the internal scanner fallback path.
package toml
