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

package toml

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"unsafe"
)

func TestParseDocumentBytesRoundTrip(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{
		"testdata/corpus/cargo.lock",
		"testdata/tokens/basic.toml",
		"testdata/tokens/comments.toml",
	} {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			body := mustReadRepoFile(t, rel)
			doc, err := ParseDocument(body)
			if err != nil {
				t.Fatalf("ParseDocument(%s) error = %v", rel, err)
			}
			if !bytes.Equal(doc.Bytes(), body) {
				t.Fatalf("Bytes() != input for %s", rel)
			}
			if !bytes.Equal(doc.Raw(), body) {
				t.Fatalf("Raw() != input for %s", rel)
			}
		})
	}
}

func TestDocumentGetSupportsDottedAndQuotedPaths(t *testing.T) {
	t.Parallel()

	doc, err := ParseDocument([]byte("title = 'TOML'\n[owner]\n\"first.name\" = \"Tom\"\n"))
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	if got, ok := doc.Get("title"); !ok || got != "TOML" {
		t.Fatalf("Get(title) = (%#v, %v), want TOML,true", got, ok)
	}
	if got, ok := doc.Get("owner.\"first.name\""); !ok || got != "Tom" {
		t.Fatalf("Get(owner.\"first.name\") = (%#v, %v), want Tom,true", got, ok)
	}
}

func TestDocumentSetSameKindMinimalDiffAndStyle(t *testing.T) {
	t.Parallel()

	input := []byte("# leading\nname = 'old' # keep comment\ncount = 1\n")
	doc, err := ParseDocument(input)
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	if err := doc.Set("name", "new"); err != nil {
		t.Fatalf("Set(name) error = %v", err)
	}
	got := string(doc.Bytes())
	want := "# leading\nname = 'new' # keep comment\ncount = 1\n"
	if got != want {
		t.Fatalf("Bytes after Set(name) = %q, want %q", got, want)
	}
	if strings.Count(got, "# keep comment") != 1 {
		t.Fatalf("inline comment not preserved in %q", got)
	}
}

func TestDocumentSetKindChangeCanonicalizesOnlyValue(t *testing.T) {
	t.Parallel()

	input := []byte("enabled = true # keep\n")
	doc, err := ParseDocument(input)
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	if err := doc.Set("enabled", "yes"); err != nil {
		t.Fatalf("Set(enabled) error = %v", err)
	}
	want := "enabled = \"yes\" # keep\n"
	if got := string(doc.Bytes()); got != want {
		t.Fatalf("Bytes after kind-changing Set = %q, want %q", got, want)
	}
}

func TestDocumentInsertAfterAndDelete(t *testing.T) {
	t.Parallel()

	input := []byte("title = \"demo\"\ncount = 1\n")
	doc, err := ParseDocument(input)
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	if err := doc.InsertAfter("title", "enabled", true); err != nil {
		t.Fatalf("InsertAfter error = %v", err)
	}
	if err := doc.Delete("count"); err != nil {
		t.Fatalf("Delete error = %v", err)
	}
	if got, ok := doc.Get("enabled"); !ok || got != true {
		t.Fatalf("Get(enabled) = (%#v, %v), want true,true", got, ok)
	}
	if got, ok := doc.Get("count"); ok {
		t.Fatalf("Get(count) after Delete = (%#v, true), want missing", got)
	}
	want := "title = \"demo\"\nenabled = true\n"
	if got := string(doc.Bytes()); got != want {
		t.Fatalf("Bytes after InsertAfter/Delete = %q, want %q", got, want)
	}
}

func TestParseDocumentCopiesInputIntoDocumentRaw(t *testing.T) {
	t.Parallel()

	body := []byte("name = \"demo\"\n")
	doc, err := ParseDocument(body)
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	body[0] = 'N'
	if got := doc.Bytes(); got[0] != 'n' {
		t.Fatalf("Bytes() should preserve parsed bytes after caller mutates input, got=%q", got)
	}
	if got := doc.Raw(); got[0] != 'n' {
		t.Fatalf("Raw() should preserve parsed bytes after caller mutates input, got=%q", got)
	}
}

func TestDocumentBytesNilSafe(t *testing.T) {
	t.Parallel()

	var doc *Document
	if got := doc.Bytes(); got != nil {
		t.Fatalf("nil Document Bytes() = %v, want nil", got)
	}
	if got := doc.Raw(); got != nil {
		t.Fatalf("nil Document Raw() = %v, want nil", got)
	}
}

func TestParseDocumentRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	_, err := ParseDocument([]byte("broken = [\n"))
	if err == nil {
		t.Fatal("ParseDocument returned nil error for invalid input")
	}
	if !errors.Is(err, io.EOF) {
		t.Logf("ParseDocument invalid input error = %T(%v)", err, err)
	}
}

func TestDocumentBytesAllocationsUntouched(t *testing.T) {
	body := mustReadRepoFile(t, "testdata/corpus/cargo.lock")
	doc, err := ParseDocument(body)
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	if got := testing.AllocsPerRun(100, func() {
		_ = doc.Bytes()
	}); got != 0 {
		t.Fatalf("Document.Bytes() allocs/run = %.0f, want 0", got)
	}
}

func TestParseDocumentEntryRawSlicesAliasDocumentArena(t *testing.T) {
	t.Parallel()

	doc, err := ParseDocument([]byte("name = \"demo\"\n"))
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	if len(doc.entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(doc.entries))
	}
	entry := doc.entries[0]
	if got := string(entry.valueRaw); got != "\"demo\"" {
		t.Fatalf("entry.valueRaw = %q, want quoted demo", got)
	}
	if !sliceAliasesBase(entry.valueRaw, doc.raw) {
		t.Fatalf("entry.valueRaw does not alias Document raw arena")
	}
}

func TestDocumentParserPathKeyMatchesSharedParser(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"bare":                 "fruit",
		"dotted":               "fruit.apple.taste",
		"quoted dot segment":   `fruit."apple.color"`,
		"single quoted":        "fruit.'apple taste'",
		"quoted empty segment": `fruit.""`,
		"spaces around dot":    "fruit . apple . taste",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wantParts, err := parseDottedKey([]byte(input))
			if err != nil {
				t.Fatalf("parseDottedKey(%q) error = %v", input, err)
			}
			var parser documentParser
			got, err := parser.parseDottedPathKey([]byte(input))
			if err != nil {
				t.Fatalf("parseDottedPathKey(%q) error = %v", input, err)
			}
			if want := joinDocumentPath(wantParts); got != want {
				t.Fatalf("parseDottedPathKey(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestDocumentParserHeaderPathKeyTrimsHeaderSyntax(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw   string
		array bool
		want  string
	}{
		"table":       {raw: "[package.metadata]", want: "package\x00metadata"},
		"array table": {raw: "[[package]]", array: true, want: "package"},
		"spaced":      {raw: "[ fruit . apple ]", want: "fruit\x00apple"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var parser documentParser
			got, err := parser.parseHeaderPathKey([]byte(tc.raw), tc.array)
			if err != nil {
				t.Fatalf("parseHeaderPathKey(%q) error = %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parseHeaderPathKey(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDocumentInsertionCachesPathKey(t *testing.T) {
	t.Parallel()

	doc, err := ParseDocument([]byte("title = \"demo\"\n"))
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	if err := doc.InsertAfter("title", "pandaemonium.edited", true); err != nil {
		t.Fatalf("InsertAfter error = %v", err)
	}
	if len(doc.insertions) != 1 {
		t.Fatalf("len(insertions) = %d, want 1", len(doc.insertions))
	}
	ins := doc.insertions[0]
	if want := joinDocumentPath(ins.path); ins.pathKey != want {
		t.Fatalf("insertion pathKey = %q, want %q", ins.pathKey, want)
	}
}

func TestDocumentEntryForPathKey(t *testing.T) {
	t.Parallel()

	doc, err := ParseDocument([]byte("title = \"demo\"\n"))
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}
	entry, ok := doc.entryForPathKey("title")
	if !ok {
		t.Fatal("entryForPathKey(title) ok = false, want true")
	}
	if got := string(entry.valueRaw); got != "\"demo\"" {
		t.Fatalf("entryForPathKey(title).valueRaw = %q, want quoted demo", got)
	}
	if entry, ok := doc.entryForPathKey("missing"); ok || entry != nil {
		t.Fatalf("entryForPathKey(missing) = (%#v, %v), want nil,false", entry, ok)
	}
	var nilDoc *Document
	if entry, ok := nilDoc.entryForPathKey("title"); ok || entry != nil {
		t.Fatalf("nil entryForPathKey = (%#v, %v), want nil,false", entry, ok)
	}
}

func TestDocumentEntryHintBounded(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input []byte
		want  int
	}{
		"empty":       {input: nil, want: 0},
		"single line": {input: []byte("a = 1"), want: 1},
		"lines":       {input: []byte("a = 1\nb = 2\n"), want: 3},
		"tiny dense":  {input: []byte("\n\n\n\n\n\n\n\n\n\n\n\n"), want: 4},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := documentEntryHint(tc.input); got != tc.want {
				t.Fatalf("documentEntryHint(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestDocumentMutationAPIQuotedDottedPath(t *testing.T) {
	t.Parallel()

	body := []byte("fruit.\"apple.color\" = \"red\"\nfruit.\"apple.taste.sweet\" = true\n")
	doc, err := ParseDocument(body)
	if err != nil {
		t.Fatalf("ParseDocument error = %v", err)
	}

	if got, ok := doc.Get(`fruit."apple.color"`); !ok || got != "red" {
		t.Fatalf(`Get(%q) = %v, %v; want "red", true`, `fruit."apple.color"`, got, ok)
	}
	if got, ok := doc.Get(`fruit."apple.taste.sweet"`); !ok || got != true {
		t.Fatalf(`Get(%q) = %v, %v; want true, true`, `fruit."apple.taste.sweet"`, got, ok)
	}

	if err := doc.Set(`fruit."apple.color"`, "green"); err != nil {
		t.Fatalf("Set color error = %v", err)
	}
	if err := doc.InsertAfter(`fruit."apple.color"`, `fruit."apple.size"`, 3); err != nil {
		t.Fatalf("InsertAfter size error = %v", err)
	}
	if err := doc.Delete(`fruit."apple.taste.sweet"`); err != nil {
		t.Fatalf("Delete taste error = %v", err)
	}
	if _, ok := doc.Get(`fruit."apple.taste.sweet"`); ok {
		t.Fatal(`Get("fruit.\"apple.taste.sweet\"") unexpectedly succeeded after Delete`)
	}

	want := "fruit.\"apple.color\" = \"green\"\n" +
		"fruit.\"apple.size\" = 3\n"
	if got := string(doc.Bytes()); got != want {
		t.Fatalf("Bytes() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func sliceAliasesBase(sub, base []byte) bool {
	if len(sub) == 0 || len(base) == 0 {
		return false
	}
	subPtr := uintptr(unsafe.Pointer(unsafe.SliceData(sub)))
	basePtr := uintptr(unsafe.Pointer(unsafe.SliceData(base)))
	return subPtr >= basePtr && subPtr < basePtr+uintptr(len(base))
}

func TestDocumentParseEqualsDecoderTokenStream(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{
		"testdata/tokens/basic.toml",
		"testdata/tokens/comments.toml",
	} {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			body := mustReadRepoFile(t, rel)
			want, err := collectDecoderTokenTrace(body)
			if err != nil {
				t.Fatalf("collectDecoderTokenTrace(%s) error = %v", rel, err)
			}
			got, err := collectDocumentTokenTrace(body)
			if err != nil {
				t.Fatalf("collectDocumentTokenTrace(%s) error = %v", rel, err)
			}
			if !tokenTraceEqual(got, want) {
				t.Fatalf("document token trace diverged from Decoder.ReadToken\n got: %#v\nwant: %#v", got, want)
			}
		})
	}
}

type documentTokenTrace struct {
	Kind   TokenKind
	Bytes  string
	Offset int
	Span   [2]int
}

func collectDecoderTokenTrace(body []byte) ([]documentTokenTrace, error) {
	dec := NewDecoderBytes(body)
	var out []documentTokenTrace
	for {
		tok, err := dec.ReadToken()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, documentTokenTrace{Kind: tok.Kind, Bytes: string(tok.Bytes), Offset: tok.Offset, Span: tokenSpan(tok)})
	}
}

func collectDocumentTokenTrace(body []byte) ([]documentTokenTrace, error) {
	raw := append([]byte(nil), body...)
	p := &documentParser{data: raw, dec: NewDecoderBytes(raw)}
	var out []documentTokenTrace
	for {
		tok, err := p.readToken()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, documentTokenTrace{Kind: tok.Kind, Bytes: string(tok.Bytes), Offset: tok.Offset, Span: tok.span})
	}
}

func tokenTraceEqual(a, b []documentTokenTrace) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDocumentSetStringControlCharacters(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source  string
		value   string
		want    string
		wantErr error
	}{
		"success: literal style preserved for safe value": {
			source: "title = 'old'\n",
			value:  "new value",
			want:   "title = 'new value'\n",
		},
		"success: literal falls back to basic on newline": {
			source: "title = 'old'\n",
			value:  "line1\nline2",
			want:   "title = \"line1\\nline2\"\n",
		},
		"success: literal falls back to basic on bell": {
			source: "title = 'old'\n",
			value:  "bell\x07end",
		},
		"success: basic escapes delete": {
			source: "title = \"old\"\n",
			value:  "del\x7fend",
		},
		"error: invalid utf8": {
			source:  "title = 'old'\n",
			value:   string([]byte{'x', 0xff}),
			wantErr: errInvalidUTF8,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc, err := ParseDocument([]byte(tc.source))
			if err != nil {
				t.Fatalf("ParseDocument(%q) error = %v", tc.source, err)
			}
			err = doc.Set("title", tc.value)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Set(%q) error = %v, want %v", tc.value, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q) error = %v", tc.value, err)
			}
			out := doc.Bytes()
			if tc.want != "" && string(out) != tc.want {
				t.Fatalf("document = %q, want %q", out, tc.want)
			}
			for _, b := range out {
				if b == 0x7f || (b < 0x20 && b != '\n' && b != '\t') {
					t.Fatalf("document contains raw control byte %#x: %q", b, out)
				}
			}
			var decoded map[string]string
			if err := Unmarshal(out, &decoded); err != nil {
				t.Fatalf("Unmarshal(%q) error = %v", out, err)
			}
			if got := decoded["title"]; got != tc.value {
				t.Fatalf("round trip = %q, want %q", got, tc.value)
			}
		})
	}
}
