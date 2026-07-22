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

package scan

import (
	"strings"
	"testing"
)

// scan_test.go contains golden table-driven tests for the exported
// scan kernels and for the LimitError type. Test keys follow the
// AGENTS.md convention of a "success:" or "error:" prefix. Cases are
// deliberately concentrated on 8-byte SWAR-stride boundaries (len 7/8/9
// and 15/16/17) because those are where the unsafe.Pointer 64-bit load
// path differs from the tail scalar loop.

func TestScanBareKey(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":              {[]byte(""), 0},
		"success:single_letter":      {[]byte("a"), 1},
		"success:single_digit":       {[]byte("9"), 1},
		"success:underscore":         {[]byte("_"), 1},
		"success:hyphen":             {[]byte("-"), 1},
		"success:all_class_short":    {[]byte("foo_bar-123"), 11},
		"success:stops_at_dot":       {[]byte("foo.bar"), 3},
		"success:stops_at_equals":    {[]byte("key="), 3},
		"success:stops_at_space":     {[]byte("abc def"), 3},
		"success:first_byte_invalid": {[]byte(".x"), 0},
		"success:boundary_7_bytes":   {[]byte("abcdefg"), 7},
		"success:boundary_8_bytes":   {[]byte("abcdefgh"), 8},
		"success:boundary_9_bytes":   {[]byte("abcdefghi"), 9},
		"success:break_at_byte_8":    {[]byte("abcdefgh.ijk"), 8},
		"success:break_at_byte_7":    {[]byte("abcdefg.hijk"), 7},
		"success:break_at_byte_15":   {[]byte("abcdefghijklmno.pqr"), 15},
		"success:high_bit_byte":      {append([]byte("abc"), 0x80), 3},
		"success:nul_terminator":     {append([]byte("abc"), 0x00), 3},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ScanBareKey(tc.input)
			if got != tc.want {
				t.Errorf("ScanBareKey(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestScanBasicString(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":                  {[]byte(""), 0},
		"success:no_terminator":          {[]byte("hello world"), 11},
		"success:terminator_at_0":        {[]byte("\"foo"), 0},
		"success:backslash_at_0":         {[]byte("\\nfoo"), 0},
		"success:terminator_at_3":        {[]byte("foo\"bar"), 3},
		"success:backslash_at_3":         {[]byte("foo\\nbar"), 3},
		"success:terminator_at_7":        {[]byte("abcdefg\""), 7},
		"success:terminator_at_8":        {[]byte("abcdefgh\""), 8},
		"success:terminator_at_9":        {[]byte("abcdefghi\""), 9},
		"success:high_bit_clean":         {append([]byte("aé"), '"'), 3}, // é = 2 bytes
		"success:long_then_quote":        {[]byte(strings.Repeat("x", 100) + "\""), 100},
		"success:long_then_backslash":    {[]byte(strings.Repeat("x", 100) + "\\"), 100},
		"success:long_no_term":           {[]byte(strings.Repeat("x", 100)), 100},
		"success:byte_0xff":              {[]byte{0xff, '"'}, 1},
		"success:backslash_before_quote": {[]byte("\\\""), 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ScanBasicString(tc.input)
			if got != tc.want {
				t.Errorf("ScanBasicString(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestScanBasicStringEscape(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":                  {[]byte(""), 0},
		"success:no_escape":              {[]byte("hello world"), 11},
		"success:quote_at_0":             {[]byte("\"foo"), 0},
		"success:backslash_at_0":         {[]byte("\\nfoo"), 0},
		"success:quote_at_8":             {[]byte("abcdefgh\""), 8},
		"success:backslash_at_9":         {[]byte("abcdefghi\\"), 9},
		"success:nul_control":            {append([]byte("abc"), 0x00), 3},
		"success:unit_separator_control": {append([]byte("abc"), 0x1f), 3},
		"success:space_is_body_text":     {[]byte("abc def"), 7},
		"success:del_is_body_text":       {append([]byte("abc"), 0x7f), 4},
		"success:high_bit_at_0":          {[]byte{0x80, 'a'}, 0},
		"success:high_bit_after_ascii":   {append([]byte("abc"), 0x80), 3},
		"success:long_then_high_bit":     {append([]byte(strings.Repeat("x", 100)), 0x80), 100},
		"success:long_no_escape":         {[]byte(strings.Repeat("x", 100)), 100},
		"success:control_before_quote":   {[]byte{'a', 'b', 0x01, '"'}, 2},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ScanBasicStringEscape(tc.input)
			if got != tc.want {
				t.Errorf("ScanBasicStringEscape(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestScanBasicStringStrict(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":                   {[]byte(""), 0},
		"success:no_slow_byte":            {[]byte("hello world\t"), 12},
		"success:quote_at_0":              {[]byte("\"foo"), 0},
		"success:backslash_at_0":          {[]byte("\\nfoo"), 0},
		"success:quote_at_8":              {[]byte("abcdefgh\""), 8},
		"success:backslash_at_9":          {[]byte("abcdefghi\\"), 9},
		"success:tab_is_body_text":        {[]byte("abc\tdef"), 7},
		"success:newline_is_control":      {[]byte("abc\ndef"), 3},
		"success:carriage_return_control": {[]byte("abc\rdef"), 3},
		"success:nul_control":             {append([]byte("abc"), 0x00), 3},
		"success:unit_separator_control":  {append([]byte("abc"), 0x1f), 3},
		"success:space_is_body_text":      {[]byte("abc def"), 7},
		"success:del_is_slow_byte":        {append([]byte("abc"), 0x7f), 3},
		"success:high_bit_clean":          {append([]byte("aé"), 'z'), 4},
		"success:long_then_del":           {append([]byte(strings.Repeat("x", 100)), 0x7f), 100},
		"success:long_no_slow_byte":       {[]byte(strings.Repeat("x", 100)), 100},
		"success:tab_before_quote":        {[]byte("abc\t\"def"), 4},
		"success:control_before_quote":    {[]byte{'a', 'b', 0x01, '"'}, 2},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ScanBasicStringStrict(tc.input)
			if got != tc.want {
				t.Errorf("ScanBasicStringStrict(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestScanCommentBody(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":              {[]byte(""), 0},
		"success:plain_comment":      {[]byte("hello world"), 11},
		"success:tab_is_body_text":   {[]byte("abc\tdef"), 7},
		"success:linefeed_at_0":      {[]byte("\nnext"), 0},
		"success:carriage_at_3":      {[]byte("abc\r\n"), 3},
		"success:nul_control":        {append([]byte("abc"), 0x00), 3},
		"success:unit_separator":     {append([]byte("abc"), 0x1f), 3},
		"success:del_control":        {append([]byte("abc"), 0x7f), 3},
		"success:quote_is_body_text": {[]byte("abc\"def"), 7},
		"success:hash_is_body_text":  {[]byte("abc#def"), 7},
		"success:long_then_lf":       {[]byte(strings.Repeat("x", 100) + "\n"), 100},
		"success:long_no_stop":       {[]byte(strings.Repeat("x", 100)), 100},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ScanCommentBody(tc.input)
			if got != tc.want {
				t.Errorf("ScanCommentBody(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestScanBareValueEnd(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":                {[]byte(""), 0},
		"success:no_delimiter":         {[]byte("1979-05-27T07:32:00Z"), 20},
		"success:space_at_0":           {[]byte(" value"), 0},
		"success:tab_at_3":             {[]byte("123\t456"), 3},
		"success:cr_at_3":              {[]byte("123\r\n"), 3},
		"success:lf_at_3":              {[]byte("123\n"), 3},
		"success:comma_at_3":           {[]byte("123,456"), 3},
		"success:right_bracket_at_3":   {[]byte("123]"), 3},
		"success:right_brace_at_3":     {[]byte("123}"), 3},
		"success:hash_at_3":            {[]byte("123#comment"), 3},
		"success:equals_at_3":          {[]byte("123=456"), 3},
		"success:quote_is_not_delim":   {[]byte("12\"34,"), 5},
		"success:long_then_hash":       {[]byte(strings.Repeat("x", 100) + "#"), 100},
		"success:long_no_delimiter":    {[]byte(strings.Repeat("x", 100)), 100},
		"success:delimiter_boundary_8": {[]byte("abcdefgh,"), 8},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ScanBareValueEnd(tc.input)
			if got != tc.want {
				t.Errorf("ScanBareValueEnd(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestCountLines(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":          {[]byte(""), 0},
		"success:no_lf":          {[]byte("abc\rdef"), 0},
		"success:single_lf":      {[]byte("abc\n"), 1},
		"success:crlf_counts_lf": {[]byte("abc\r\ndef"), 1},
		"success:multiple_lf":    {[]byte("a\nb\nc\n"), 3},
		"success:nul_ignored":    {[]byte{'a', 0x00, '\n'}, 1},
		"success:long_every_10": {func() []byte {
			b := []byte(strings.Repeat("abcdefghi\n", 10))
			return b
		}(), 10},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := CountLines(tc.input)
			if got != tc.want {
				t.Errorf("CountLines(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestScanLiteralString(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":              {[]byte(""), 0},
		"success:no_terminator":      {[]byte("hello world"), 11},
		"success:terminator_at_0":    {[]byte("'foo"), 0},
		"success:terminator_at_3":    {[]byte("foo'bar"), 3},
		"success:terminator_at_7":    {[]byte("abcdefg'"), 7},
		"success:terminator_at_8":    {[]byte("abcdefgh'"), 8},
		"success:terminator_at_9":    {[]byte("abcdefghi'"), 9},
		"success:double_quote_is_ok": {[]byte("a\"b'"), 3},
		"success:backslash_is_ok":    {[]byte("a\\b'"), 3},
		"success:long_then_term":     {[]byte(strings.Repeat("x", 100) + "'"), 100},
		"success:long_no_term":       {[]byte(strings.Repeat("x", 100)), 100},
		"success:byte_0xff":          {[]byte{0xff, '\''}, 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ScanLiteralString(tc.input)
			if got != tc.want {
				t.Errorf("ScanLiteralString(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestSkipWhitespace(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":             {[]byte(""), 0},
		"success:no_whitespace":     {[]byte("foo"), 0},
		"success:one_space":         {[]byte(" foo"), 1},
		"success:one_tab":           {[]byte("\tfoo"), 1},
		"success:mixed":             {[]byte(" \t \tfoo"), 4},
		"success:all_whitespace":    {[]byte("   "), 3},
		"success:stops_at_newline":  {[]byte("  \nfoo"), 2},
		"success:newline_is_not_ws": {[]byte("\n"), 0},
		"success:cr_is_not_ws":      {[]byte("\rfoo"), 0},
		"success:long_run":          {[]byte(strings.Repeat(" ", 100) + "x"), 100},
		"success:nul_is_not_ws":     {[]byte{0x00, ' '}, 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := SkipWhitespace(tc.input)
			if got != tc.want {
				t.Errorf("SkipWhitespace(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestLocateNewline(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":          {[]byte(""), -1},
		"success:no_newline":     {[]byte("hello world"), -1},
		"success:newline_at_0":   {[]byte("\nfoo"), 0},
		"success:newline_at_3":   {[]byte("foo\nbar"), 3},
		"success:newline_at_7":   {[]byte("abcdefg\n"), 7},
		"success:newline_at_8":   {[]byte("abcdefgh\n"), 8},
		"success:newline_at_9":   {[]byte("abcdefghi\n"), 9},
		"success:cr_not_newline": {[]byte("foo\rbar"), -1},
		"success:multiple":       {[]byte("a\nb\nc"), 1},
		"success:long_then_nl":   {[]byte(strings.Repeat("x", 100) + "\n"), 100},
		"success:long_no_nl":     {[]byte(strings.Repeat("x", 100)), -1},
		"success:high_bit_byte":  {[]byte{0xff, '\n'}, 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := LocateNewline(tc.input)
			if got != tc.want {
				t.Errorf("LocateNewline(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateUTF8(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input []byte
		want  int
	}{
		"success:empty":                 {[]byte(""), 0},
		"success:pure_ascii":            {[]byte("hello world"), 11},
		"success:valid_two_byte":        {[]byte("é"), 2}, // 0xC3 0xA9
		"success:valid_three_byte":      {[]byte("€"), 3}, // 0xE2 0x82 0xAC
		"success:valid_four_byte":       {[]byte("𝄞"), 4}, // 0xF0 0x9D 0x84 0x9E
		"success:invalid_lone_cont":     {[]byte{0x80}, 0},
		"success:invalid_overlong":      {[]byte{0xC0, 0x80}, 0},
		"success:invalid_surrogate":     {[]byte{0xED, 0xA0, 0x80}, 0},
		"success:truncated_two_byte":    {[]byte{0xC3}, 0},
		"success:truncated_three_byte":  {[]byte{0xE2, 0x82}, 0},
		"success:valid_then_invalid":    {[]byte{'a', 'b', 'c', 0x80}, 3},
		"success:long_ascii":            {[]byte(strings.Repeat("x", 100)), 100},
		"success:long_ascii_then_bad":   {append([]byte(strings.Repeat("x", 100)), 0x80), 100},
		"success:mixed_ascii_multibyte": {[]byte("aébc"), 5},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ValidateUTF8(tc.input)
			if got != tc.want {
				t.Errorf("ValidateUTF8(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestLimitError_Error(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		err  *LimitError
		want string
	}{
		"success:nested_depth": {
			&LimitError{Limit: "MaxNestedDepth", Value: 1024, Span: [2]int{4096, 4100}},
			"toml: MaxNestedDepth cap exceeded (limit=1024, span=[4096,4100))",
		},
		"success:key_length": {
			&LimitError{Limit: "MaxKeyLength", Value: 65536, Span: [2]int{0, 65537}},
			"toml: MaxKeyLength cap exceeded (limit=65536, span=[0,65537))",
		},
		"success:string_length": {
			&LimitError{Limit: "MaxStringLength", Value: 16777216, Span: [2]int{12, 16777229}},
			"toml: MaxStringLength cap exceeded (limit=16777216, span=[12,16777229))",
		},
		"success:document_size": {
			&LimitError{Limit: "MaxDocumentSize", Value: 16777216, Span: [2]int{0, 16777217}},
			"toml: MaxDocumentSize cap exceeded (limit=16777216, span=[0,16777217))",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}
