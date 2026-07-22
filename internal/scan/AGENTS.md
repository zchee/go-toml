# internal/scan KNOWLEDGE BASE

## OVERVIEW

Byte scan kernels for `toml` decoder hot paths. This package owns scan
backend selection, SIMD/SWAR/NEON assembly, scalar oracles, fuzz/property tests,
and perf-gate benchmark pairs.

## WHERE TO LOOK

| Task | Location | Notes |
| --- | --- | --- |
| Public internal API | `api.go` | Sentinel contracts and `LimitError`. |
| Package contract | `doc.go`, `README.md` | Build matrix, maintenance contract, perf table. |
| Scalar helpers/oracles | `strict.go`, `naive_scan_test.go` | Single source for class predicates. |
| Basic string escape scan | `api.go`, `scan_*.go`, `marshal.go` | `ScanBasicStringEscape` finds marshal escape/fallback bytes. |
| amd64 backend | `scan_amd64.go`, `scan_amd64_single_byte.s` | SSE2 default, AVX2 via `archsimd.X86.AVX2()`. |
| arm64 backend | `scan_arm64.go`, `scan_arm64.s` | NEON, no runtime feature detect. |
| SWAR backend | `scan_swar.go`, `scan_force_swar.go` | Pure Go fallback and forced verification. |
| Dispatch tests | `dispatch*_test.go` | Tuple and backend binding checks. |
| Perf gate | `bench_test.go` | 64 KiB hard-gate benchmark pairs. |

## CONVENTIONS

- Exactly one backend binds dispatch vars for each build tuple.
- `force_swar` is a verification path, not production behavior.
- `LocateNewline` returns `-1` on miss; most other scanners return `len(s)`.
- Backends do not allocate, retain haystacks, update parser state, enforce caps,
  or construct diagnostics.
- Parser grammar changes that alter a scan class must update:
  `api.go`, `strict.go`, all backend files, oracle/property/fuzz tests,
  `bench_test.go`, and perf-gate expectations when applicable.
- Do not add a second dispatcher layer.
- For assembly edits, format `.s` files with the repo asmfmt tool.

## ANTI-PATTERNS

- Do not import TOML competitor libraries or benchmark-only packages.
- Do not change only one backend for a grammar tweak unless tests prove the
  others are unaffected.
- Do not hand-wave perf; capture backend, GOARCH, GOEXPERIMENT, CPU/host,
  command, sample count, and benchstat result.
- Do not add parser concerns here. Keep scanner narrow.

## COMMANDS

```bash
go test -count=1 ./internal/scan/
go test -tags=force_swar -count=1 ./internal/scan/
go build ./internal/scan/
go vet ./internal/scan/
go vet -unsafeptr ./internal/scan/
go tool asmfmt -w internal/scan/*.s
```
