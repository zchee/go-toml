# go-toml KNOWLEDGE BASE

## OVERVIEW

Standalone flat TOML package (`github.com/zchee/go-toml`) with one public
surface for streaming tokens, marshal/unmarshal, TOML datetime values, and
format-preserving document edits.

## WHERE TO LOOK

| Task | Location | Notes |
| --- | --- | --- |
| Streaming parser | `decoder.go`, `token.go` | `Decoder`, `Token`, `TokenKind`. |
| Facade API | `marshal.go`, `unmarshal.go`, `encoder.go`, `options.go` | `Marshal`, `Unmarshal`, `Encoder`. |
| Format-preserving edits | `document.go`, `document_test.go` | Preserve comments, whitespace, untouched spans. |
| Datetime model | `datetime.go`, `datetime_test.go` | Local vs offset TOML datetime behavior. |
| Scanner backend | `internal/scan/` | Has local AGENTS.md. SIMD/SWAR kernels. |
| Reflection cache | `internal/reflectcache/` | Internal field metadata cache. |
| Comparator benchmarks | `benchmark/` | Has local AGENTS.md. Separate module. |
| Fixtures | `testdata/` | Tokens, corpus, deterministic property seed. |

## CONVENTIONS

- Keep the public package flat: no new production subpackages beyond
  `internal/reflectcache` and `internal/scan` without a design.
- Token positions are byte offsets. `Token.Line` and `Token.Col` are populated
  for compatibility but are deprecated.
- `Decoder.Decode` is whole-document convenience before token consumption; once
  `ReadToken` starts, stay on token API or create a new decoder.
- Struct tags use `toml:"name"` and `toml:"name,omitzero"`.
- Reject `omitempty`; use `omitzero`.
- Direct struct decoding uses one immutable per-document string arena by
  default; `WithCopiedStrings` opts out.
- Local datetime into `time.Time` requires explicit `WithLocalAsUTC`.

## TESTDATA AND PERF

- Parser token goldens live in `testdata/tokens/*.toml` and
  `*.tokens.golden`.
- Property tests use `testdata/property_seed.txt` and deterministic case counts.
- Corpus parity lives under `testdata/corpus/`.
- Competitor libraries must stay inside the `benchmark/` submodule.
- Perf evidence should use comparable benchmark pairs from `benchmark/`, not a
  single ad hoc `go test -bench` sample.

## ANTI-PATTERNS

- Do not import `github.com/BurntSushi/toml` or
  `github.com/pelletier/go-toml/v2` from production or ordinary tests.
- Do not change TOML token semantics in parser code without updating scanner
  oracle/property/fuzz tests when scan classes are affected.
- Do not rewrite untouched `Document` source spans.

## COMMANDS

```bash
go test -count=1 ./...
go test -race -count=1 -shuffle=on ./...
go list -deps ./... | rg 'BurntSushi|pelletier'
go list -deps -test ./... | rg 'BurntSushi|pelletier'
```
