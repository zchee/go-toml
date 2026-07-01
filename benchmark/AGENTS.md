# benchmark KNOWLEDGE BASE

## OVERVIEW

Separate Go module for benchmark-only TOML comparator dependencies. It exists
to compare `go-toml` against external libraries without polluting the main
module's production or ordinary test dependency graph.

## WHERE TO LOOK

| Task | Location | Notes |
| --- | --- | --- |
| Module boundary | `go.mod`, `go.sum` | Owns comparator dependency graph. |
| Comparator benches | `benchmark_test.go`, `facade_bench_test.go` | Benchmarks against external TOML libs. |
| Corpus inputs | `testdata/` | Benchmark fixtures only. |
| Boundary docs | `../README.md`, `../doc.go` | Main package explains dependency isolation. |

## CONVENTIONS

- Keep this as a separate module.
- Comparator libraries belong here only:
  - `github.com/BurntSushi/toml`
  - `github.com/pelletier/go-toml/v2`
- Do not import parent package internals to cheat benchmark comparisons.
- Benchmark names should remain stable enough for `benchstat` pairing.
- Use `-mod=mod` when checking this submodule dependency graph.

## ANTI-PATTERNS

- Do not move comparator deps into the root `go.mod`.
- Do not vendor-edit comparator source.
- Do not treat benchmark-only results as production API contracts without
  package-level tests.

## COMMANDS

```bash
(cd benchmark && go test -bench=. -benchmem -run=^$ .)
(cd benchmark && go test -count=1 .)
(cd benchmark && go list -mod=mod -deps -test . | rg 'github.com/BurntSushi/toml$|github.com/pelletier/go-toml/v2$')
```
