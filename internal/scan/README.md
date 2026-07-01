# `internal/scan`

`scan` contains the byte-search kernels used by the `toml` decoder hot
loop. The package is intentionally narrow: each exported function accepts one
`[]byte`, returns a count or index, retains no references to the input slice,
and leaves all parser-level state, line/column accounting, and limit enforcement
to callers.

The package is internal because the contracts are tuned for this parser rather
than for a general-purpose byte-search API. In particular, `LocateNewline`
returns `-1` when no newline is present, while the other scanners generally
return `len(s)` for "not found" or "all bytes accepted".

## Exported kernels

| Function | Return contract | TOML role |
| --- | --- | --- |
| `ScanBareKey` | Count of the leading `[A-Za-z0-9_-]` bytes; `len(s)` when all bytes match. | Scans bare key segments. |
| `ScanBasicString` | Index of the first `"` or `\`; `len(s)` when neither byte appears. | Finds the next basic-string terminator or escape introducer. |
| `ScanBasicStringStrict` | Index of the first `"`, `\`, DEL, or C0 control other than tab; `len(s)` when absent. | Fuses single-line basic-string delimiter and control validation. |
| `ScanCommentBody` | Index of the first LF, CR, DEL, or C0 control other than tab; `len(s)` when absent. | Scans bytes after a leading `#` while preserving parser-owned CRLF semantics. |
| `ScanBareValueEnd` | Index of the first raw bare-value delimiter; `len(s)` when absent. | Finds raw value token boundaries before parser-level datetime-space joining. |
| `CountLines` | Count of LF bytes only. | Speeds lazy line/column diagnostics while keeping CR display rules in the parser. |
| `ScanLiteralString` | Index of the first `'`; `len(s)` when absent. | Finds the literal-string terminator. |
| `SkipWhitespace` | Count of leading space or tab bytes. Newline is not whitespace here. | Skips insignificant horizontal whitespace without crossing statements. |
| `LocateNewline` | Index of the first `\n`; `-1` when absent. | Finds statement boundaries and line-accounting checkpoints. |
| `ValidateUTF8` | Index of the first invalid UTF-8 sequence start; `len(s)` when valid. | Reports parser errors at the first offending byte. |

`LimitError` also lives in this package because every consumer enforces the same
shape of parser limit around scan results. The scanners themselves never enforce
limits and never return errors.

## Dispatch matrix

The public functions in `api.go` route through unexported function variables
(`scanBareIdent`, `scanBasicString`, and so on). Exactly one backend binds those
variables for a given build tuple.

| Build tuple | Backend | Notes |
| --- | --- | --- |
| `amd64 && goexperiment.simd && !force_swar` | SSE2 by default, AVX2 when `archsimd.X86.AVX2()` is true. | Implemented with `simd/archsimd` intrinsics, except the single-byte `LocateNewline` and `ScanLiteralString` loops use Plan 9 assembly to match stdlib-style `IndexByte` lowering. |
| `arm64 && !force_swar` | NEON. | Arm64 guarantees ASIMD, so dispatch is static. Assembly entry points live in `scan_arm64.s`; `ValidateUTF8` uses a NEON ASCII fast path plus a Go scalar continuation. |
| `force_swar` on any architecture | SWAR fallback. | Test-only override for exercising the pure-Go backend on SIMD-capable hosts. |
| Other tuples, including `amd64` without `goexperiment.simd` | SWAR fallback. | Pure Go 64-bit word scans plus scalar loops for predicates where SWAR would risk false positives. |

Do not add a second dispatcher layer around these function variables. The current
shape keeps call sites simple and makes backend selection visible in tests.

## Backend design notes

- The SWAR backend uses 64-bit unaligned loads and the classic `hasZeroByte`
  trick for first-match scans. Its index extraction relies on little-endian word
  ordering; `doc.go` documents the big-endian requirement if a future port adds
  such a target.
- `ScanBareKey` uses a low/high-nibble VTBL bitset classifier on arm64 and
  explicit range/equality classification on amd64; `SkipWhitespace` uses direct
  equality classification on SIMD backends. Both use scalar predicate loops in
  the SWAR backend. That is deliberate: the `hasZeroByte` false-positive
  behavior is safe for "find first match" scans but not for "find first
  non-match" scans.
- SIMD UTF-8 validation is intentionally an ASCII fast path followed by scalar
  `unicode/utf8.DecodeRune` handling. A full SIMD UTF-8 state machine would need
  a separate benchmark-backed design before it belongs here.
- The package must not import competitor TOML parsers or benchmark-only
  packages. Prior benchmarking found the current scanner path faster for these
  hot spots unless fresh evidence says otherwise.

## Correctness and verification

The scanner has three correctness layers:

1. `naive_scan_test.go` defines the scalar oracle for every kernel.
2. `scan_test.go`, `dispatch_*_test.go`, and `property_test.go` compare the
   active dispatcher and forced backends against that oracle.
3. `fuzz_test.go` contains one fuzz target per exported kernel.
4. `bench_test.go` contains the 64 KiB hard-gate pairs plus small-size
   informational benches for the newer fused/comment/value/count kernels.

For a focused edit, run at least:

```bash
go test -count=1 ./internal/scan/
go test -tags=force_swar -count=1 ./internal/scan/
git diff --check
```

For backend, unsafe, or assembly changes, also run the package build/vet gates:

```bash
go build ./internal/scan/
go vet ./internal/scan/
go vet -unsafeptr ./internal/scan/
```

For assembly edits, format the relevant `.s` files with the repository's asmfmt
tool before committing. For performance-sensitive backend changes, use the scan
benchmarks in `bench_test.go` instead of relying on a single `go test -bench`
sample.

## Editing rules

- Keep the public surface limited to parser-proven scan kernels plus
  `LimitError`; new primitives must be wired into parser code, oracle tests,
  fuzz/property coverage, and perf gates in the same change.
- Preserve each function's sentinel contract; do not normalize
  `LocateNewline` to `len(s)`.
- Keep parser concerns out of this package. The scanner does not allocate parser
  nodes, update decoder state, enforce DoS caps, or construct diagnostics.
- Treat `force_swar` as a verification tag, not a production feature flag.
- Update or add oracle/property/fuzz coverage before changing scan semantics.
- Do not hand-wave performance claims. Capture comparable before/after benchmark
  evidence and note the exact backend, `GOARCH`, `GOEXPERIMENT`, and CPU.
