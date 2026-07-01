# go-toml

`go-toml` (`github.com/zchee/go-toml`) is a flat, high-performance TOML package.
It keeps one public package while exposing four surfaces; parsing paths share
the same scanner and parser, and all surfaces share the same TOML value model:

- a streaming token API: `Decoder`, `Token`, and `TokenKind`;
- a facade API: `Marshal`, `Unmarshal`, `Encoder`, `MarshalOptions`, and
  `UnmarshalOptions`;
- TOML datetime types and parsers: `LocalDateTime`, `LocalDate`, `LocalTime`,
  `ParseDateTime`, and `ParseDateTimeAsTime`;
- a format-preserving edit API: `ParseDocument` and `Document`.

Internal scanner and reflection-cache packages stay under `internal/`.
The approved package shape is intentionally small:

```text
github.com/zchee/go-toml
github.com/zchee/go-toml/internal/reflectcache
github.com/zchee/go-toml/internal/scan
```

## Installation

```bash
go get github.com/zchee/go-toml
```

## Facade API

Use `Unmarshal` and `Marshal` for ordinary struct, map, scalar, array, and
TOML datetime values:

```go
package main

import "github.com/zchee/go-toml"

type Config struct {
	Title  string `toml:"title"`
	Server struct {
		Port int `toml:"port"`
	} `toml:"server"`
}

func load(data []byte) (Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func save(cfg Config) ([]byte, error) {
	return toml.Marshal(cfg)
}
```

`Encoder` writes one TOML document to an `io.Writer`:

```go
enc := toml.NewEncoder(w)
err := enc.Encode(cfg)
```

`Decoder.Decode` reads one TOML document from a fresh decoder into a Go value:

```go
dec := toml.NewDecoder(r)
err := dec.Decode(&cfg)
```

Types can bypass reflection by implementing `MarshalerTo` or
`UnmarshalerFrom`. Struct tags use `toml:"name"` and `toml:"name,omitzero"`.
The package intentionally rejects `omitempty` in TOML tags with a typed error;
use `omitzero` for zero-value omission.

`WithDefaultOptions` returns explicit zero-value option structs for call sites
that want stable option values:

```go
marshalOptions, unmarshalOptions := toml.WithDefaultOptions()
out, err := marshalOptions.Marshal(cfg)
err = unmarshalOptions.Unmarshal(out, &cfg)
```

For direct struct decoding, escape-free TOML string values share one immutable
per-document string arena by default. This keeps allocation count low while
preventing decoded strings from aliasing caller-owned `[]byte` input. Escaped
strings still use an independent decoded copy. Use `WithCopiedStrings` when the
destination should not retain one document-sized arena after decoding:

```go
opts := toml.UnmarshalOptions{
	DecoderOptions: []toml.Option{toml.WithCopiedStrings()},
}
err := opts.Unmarshal(data, &cfg)
```

## Streaming decoder

Use `Decoder` when callers need token-level TOML inspection without binding into
Go values:

```go
dec := toml.NewDecoderBytes(data)
for {
	tok, err := dec.ReadToken()
	if err == io.EOF {
		break
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s %q at byte %d\n", tok.Kind, tok.Bytes, tok.Offset)
}
```

`NewDecoderBytes` lets token byte slices alias the caller-owned source buffer.
`NewDecoder` owns an internal buffer for `io.Reader` input. In both cases, copy
`Token.Bytes` if the bytes must outlive the next token read. `Token.Offset` is
the token's byte offset in the source document. `WithLimits`
configures parser caps for document size, nesting depth, key length, and string
length; cap violations return `*LimitError`.

Migration note: token positions are byte-offset based. `Token.Line` and
`Token.Col` remain populated for source compatibility, but they are deprecated;
new consumers should retain `Token.Offset` and derive line/column from the
original source only when presenting diagnostics. Structured parser errors still
include line/column text for user-facing error messages.

`Decode` is a whole-document convenience and is only valid before token
consumption starts. If a caller has already called `ReadToken`, continue with the
token API or create a new decoder before calling `Decode`.

## Datetime model

TOML local datetime forms do not carry an offset, so the package keeps them as
explicit TOML-local values:

- `LocalDateTime`
- `LocalDate`
- `LocalTime`

Offset datetimes parse to `time.Time` with the numeric offset preserved through
`time.FixedZone`:

```go
v, err := toml.ParseDateTime([]byte("2026-05-17T07:08:09+09:00"))
// v is a time.Time for offset date-times.
```

Decoding local datetime forms into `time.Time` returns
`*LocalTimeIntoTimeError` unless the caller opts in to UTC interpretation:

```go
opts := toml.UnmarshalOptions{
	DecoderOptions: []toml.Option{toml.WithLocalAsUTC()},
}
err := opts.Unmarshal(data, &dst)
```

## Format-preserving documents

`ParseDocument` keeps raw source bytes and parsed spans so untouched documents
round-trip byte-for-byte. Use it for edit paths that must preserve comments,
whitespace, ordering, and unchanged lexical style:

```go
doc, err := toml.ParseDocument(data)
if err != nil {
	return err
}

if port, ok := doc.Get("server.port"); ok {
	fmt.Println("old port:", port)
}

if err := doc.Set("server.port", 9443); err != nil {
	return err
}
if err := doc.InsertAfter("server.port", "server.host", "127.0.0.1"); err != nil {
	return err
}
if err := doc.Delete("legacy.enabled"); err != nil {
	return err
}

updated := doc.Bytes()
```

`Document.Raw` returns the document's original byte arena; treat it as read-only
or copy it before mutating. `Document.Bytes` returns the current document bytes;
on an untouched document it returns the raw bytes without rewriting. Setting a
value of the same TOML kind preserves the existing lexical style where the
parser recorded enough style information. Kind-changing writes use canonical
TOML emission.

## Build tags and dependency boundaries

One build tag and one benchmark submodule matter for this package:

- `force_swar` selects the pure-Go scanner fallback and is used by scanner
  verification.
- `benchmark/` is a separate Go module that owns benchmark-only comparator
  dependencies on `github.com/BurntSushi/toml` and
  `github.com/pelletier/go-toml/v2`.

The comparator libraries must not enter production or ordinary test dependency
graphs. Re-check the boundary with:

```bash
go list -deps ./... | rg 'BurntSushi|pelletier'
go list -deps -test ./... | rg 'BurntSushi|pelletier'
(cd benchmark && go list -mod=mod -deps -test . | rg 'github.com/BurntSushi/toml$|github.com/pelletier/go-toml/v2$')
```

The first two commands should print nothing. The benchmark submodule command
should print both comparator roots.

## Verification

Useful local checks:

```bash
# Full suite (native scan backend).
go test ./...

# Dependency boundary: competitors must stay benchmark-only.
go test -run TestCompetitorDependenciesAreBenchOnly -count=1 .

# Pure-Go SWAR scanner fallback under the race detector.
go test -tags=force_swar -race -count=1 ./internal/scan ./...

# Comparator benchmarks against BurntSushi and pelletier (separate module).
(cd benchmark && go test -bench . -benchmem .)
```

The scanner ships three backends selected by build tag and target
architecture: arm64 SIMD, amd64 SIMD (`GOEXPERIMENT=simd`), and a portable
pure-Go SWAR fallback (`-tags force_swar`). Cross-compile the amd64 SIMD path
with:

```bash
GOARCH=amd64 GOEXPERIMENT=simd go build ./...
```
