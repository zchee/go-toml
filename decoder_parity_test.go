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
	"bufio"
	"bytes"
	"errors"
	rand "math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	propertyCases    = 20_000
	propertyMaxLen   = 1024
	propertySeedPath = "testdata/property_seed.txt"
)

func loadPropertySeed(tb testing.TB) uint64 {
	tb.Helper()
	f, err := os.Open(propertySeedPath)
	if err != nil {
		tb.Fatalf("open %s: %v", propertySeedPath, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			tb.Fatalf("close %s: %v", propertySeedPath, err)
		}
	}()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := strconv.ParseUint(line, 10, 64)
		if err != nil {
			tb.Fatalf("parse seed %q: %v", line, err)
		}
		return v
	}
	if err := sc.Err(); err != nil {
		tb.Fatalf("scan %s: %v", propertySeedPath, err)
	}
	tb.Fatalf("no seed value in %s", propertySeedPath)
	return 0
}

func newPropertyRand(seed uint64, label string) *rand.Rand {
	var labelHash uint64 = 0xcbf29ce484222325
	for _, c := range []byte(label) {
		labelHash ^= uint64(c)
		labelHash *= 0x100000001b3
	}
	return rand.New(rand.NewPCG(seed, labelHash))
}

func runDecoderParityProperty(t *testing.T, label string, data []byte) {
	t.Helper()
	gotTokens, gotErr := readAllTokens(NewDecoderBytes(data))
	wantTokens, wantErr := readAllTokens(NewDecoder(strings.NewReader(string(data))))

	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("%s error mismatch: bytes=%v reader=%v input=%x", label, gotErr, wantErr, data)
	}
	if gotErr != nil && wantErr != nil && gotErr.Error() != wantErr.Error() {
		t.Fatalf("%s error text mismatch: bytes=%v reader=%v input=%x", label, gotErr, wantErr, data)
	}
	if len(gotTokens) != len(wantTokens) {
		t.Fatalf("%s token count mismatch: bytes=%d reader=%d input=%x", label, len(gotTokens), len(wantTokens), data)
	}
	for i := range gotTokens {
		if gotTokens[i].Kind != wantTokens[i].Kind {
			t.Fatalf("%s token[%d] kind mismatch: bytes=%q reader=%q input=%x", label, i, gotTokens[i].Kind, wantTokens[i].Kind, data)
		}
		if !slices.Equal(gotTokens[i].Bytes, wantTokens[i].Bytes) {
			t.Fatalf("%s token[%d] bytes mismatch: bytes=%x reader=%x input=%x", label, i, gotTokens[i].Bytes, wantTokens[i].Bytes, data)
		}
		if gotTokens[i].Offset != wantTokens[i].Offset {
			t.Fatalf("%s token[%d] offset mismatch: bytes=%d reader=%d input=%x", label, i, gotTokens[i].Offset, wantTokens[i].Offset, data)
		}
	}
}

func runDecoderTokenStreamInvariant(t *testing.T, label string, data []byte) {
	t.Helper()
	runDecoderParityProperty(t, label, data)
	wantTokens, wantErr := readAllTokens(NewDecoder(strings.NewReader(string(data))))
	gotTokens, gotErr := readAllTokens(NewDecoderBytes(data))
	assertTokenStreamInvariants(t, label, data, gotTokens, gotErr)
	assertTokenStreamInvariants(t, label, data, wantTokens, wantErr)
}

func assertTokenStreamInvariants(t *testing.T, label string, data []byte, tokens []Token, err error) {
	t.Helper()
	if err != nil {
		return
	}
	prevOffset := -1
	for i, tok := range tokens {
		if tok.Offset < 0 || tok.Offset > len(data) {
			t.Fatalf("%s token[%d] invalid offset: %d input_len=%d input=%x", label, i, tok.Offset, len(data), data)
		}
		if i > 0 && tok.Offset <= prevOffset {
			t.Fatalf("%s token[%d] offset non-increasing: prev=%d cur=%d input=%x", label, i, prevOffset, tok.Offset, data)
		}
		if len(tok.Bytes) == 0 {
			t.Fatalf("%s token[%d] has empty bytes: offset=%d input=%x", label, i, tok.Offset, data)
		}
		if end := tok.Offset + len(tok.Bytes); end > len(data) {
			t.Fatalf("%s token[%d] span exceeds input: span=[%d,%d) input_len=%d input=%x", label, i, tok.Offset, end, len(data), data)
		}
		prevOffset = tok.Offset
	}
}

func TestDirectDecodeParity(t *testing.T) {
	t.Run("duration from integer", func(t *testing.T) {
		type config struct {
			Value time.Duration `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, "value = 5400000000000\n")
		if got.Value != 90*time.Minute {
			t.Fatalf("Value = %v, want %v", got.Value, 90*time.Minute)
		}
	})

	t.Run("int array", func(t *testing.T) {
		type config struct {
			Value [3]int `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, "value = [1, 2, 3]\n")
		if got.Value != [3]int{1, 2, 3} {
			t.Fatalf("Value = %#v, want %#v", got.Value, [3]int{1, 2, 3})
		}
	})

	t.Run("float slice", func(t *testing.T) {
		type config struct {
			Value []float64 `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, "value = [1, 2.5, 3]\n")
		want := []float64{1, 2.5, 3}
		if !reflect.DeepEqual(got.Value, want) {
			t.Fatalf("Value = %#v, want %#v", got.Value, want)
		}
	})

	t.Run("string array", func(t *testing.T) {
		type config struct {
			Value [2]string `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, `value = ["alpha", 'beta']`+"\n")
		if got.Value != [2]string{"alpha", "beta"} {
			t.Fatalf("Value = %#v, want %#v", got.Value, [2]string{"alpha", "beta"})
		}
	})

	t.Run("bytes from basic string", func(t *testing.T) {
		got := runDirectDecodeBytes(t, `value = "abc"`+"\n")
		if !bytes.Equal(got, []byte("abc")) {
			t.Fatalf("Value = %q, want %q", got, []byte("abc"))
		}
	})

	t.Run("bytes from escaped basic string", func(t *testing.T) {
		got := runDirectDecodeBytes(t, `value = "a\nb"`+"\n")
		if !bytes.Equal(got, []byte("a\nb")) {
			t.Fatalf("Value = %q, want %q", got, []byte("a\nb"))
		}
	})

	t.Run("bytes from literal string", func(t *testing.T) {
		got := runDirectDecodeBytes(t, `value = 'a\nb'`+"\n")
		if !bytes.Equal(got, []byte(`a\nb`)) {
			t.Fatalf("Value = %q, want %q", got, []byte(`a\nb`))
		}
	})

	t.Run("duration from string", func(t *testing.T) {
		type config struct {
			Value time.Duration `toml:"value"`
		}
		var got config
		if err := Unmarshal([]byte(`value = "1h30m"`+"\n"), &got); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if got.Value != 90*time.Minute {
			t.Fatalf("Value = %v, want %v", got.Value, 90*time.Minute)
		}
	})
}

func TestDirectDecodeBytesCopyStringsOption(t *testing.T) {
	t.Run("aliases input by default", func(t *testing.T) {
		data := []byte(`value = "abc"` + "\n")
		value := runDirectDecodeBytesFromData(t, data)
		value[0] = 'z'
		if !bytes.Contains(data, []byte(`"zbc"`)) {
			t.Fatalf("decoded bytes did not alias input: data = %q", data)
		}
	})

	t.Run("copies when requested", func(t *testing.T) {
		data := []byte(`value = "abc"` + "\n")
		value := runDirectDecodeBytesFromData(t, data, WithCopiedStrings())
		value[0] = 'z'
		if bytes.Contains(data, []byte(`"zbc"`)) {
			t.Fatalf("decoded bytes mutated input despite WithCopiedStrings: data = %q", data)
		}
	})
}

func runDirectGenericDecodeParity[T any](t *testing.T, input string) T {
	t.Helper()

	data := []byte(input)
	var direct T
	if err := Unmarshal(data, &direct); err != nil {
		t.Fatalf("direct Unmarshal(%q) error = %v", input, err)
	}

	var generic T
	withDirectEligibility(t, reflect.TypeFor[T](), false, func() {
		if err := Unmarshal(data, &generic); err != nil {
			t.Fatalf("generic Unmarshal(%q) error = %v", input, err)
		}
	})

	if !reflect.DeepEqual(direct, generic) {
		t.Fatalf("direct/generic mismatch: direct=%#v generic=%#v", direct, generic)
	}
	return direct
}

func runDirectDecodeBytes(t *testing.T, input string) []byte {
	t.Helper()
	return runDirectDecodeBytesFromData(t, []byte(input))
}

func runDirectDecodeBytesFromData(t *testing.T, data []byte, opts ...Option) []byte {
	t.Helper()
	type config struct {
		Value []byte `toml:"value"`
	}
	var got config
	if err := (UnmarshalOptions{DecoderOptions: opts}).Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", data, err)
	}
	return got.Value
}

func withDirectEligibility(t *testing.T, typ reflect.Type, eligible bool, f func()) {
	t.Helper()
	old, hadOld := directStructEligibilityCache.Load(typ)
	directStructEligibilityCache.Store(typ, eligible)
	defer func() {
		if hadOld {
			directStructEligibilityCache.Store(typ, old)
			return
		}
		directStructEligibilityCache.Delete(typ)
	}()
	f()
}

func TestProperty_DecoderConstructorParity(t *testing.T) {
	t.Parallel()
	seed := loadPropertySeed(t)
	r := newPropertyRand(seed, "DecoderConstructorParity")
	buf := make([]byte, propertyMaxLen)
	for range propertyCases {
		l := r.IntN(propertyMaxLen + 1)
		for i := 0; i < l; {
			w := r.Uint64()
			for k := 0; k < 8 && i < l; k, i = k+1, i+1 {
				buf[i] = byte(w >> (8 * k))
			}
		}
		runDecoderParityProperty(t, "DecoderConstructorParity", buf[:l])
	}
}

func TestProperty_DecoderCorpusParity(t *testing.T) {
	t.Parallel()
	corpus := decoderCorpusFiles(t)
	for _, rel := range corpus {
		body := mustReadRepoFile(t, rel)
		runDecoderParityProperty(t, rel, body)
		runDecoderTokenStreamInvariant(t, rel, body)
	}
}

func decoderCorpusFiles(tb testing.TB) []string {
	tb.Helper()
	root := mustRepoPath(tb, "testdata")
	var files []string
	for _, rel := range []string{"corpus"} {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			tb.Fatalf("os.ReadDir(%s) error = %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			files = append(files, filepath.ToSlash(filepath.Join("testdata", rel, entry.Name())))
		}
	}
	slices.Sort(files)
	return files
}
