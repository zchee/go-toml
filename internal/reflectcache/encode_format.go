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

package reflectcache

import (
	"bytes"
	"math"
	"strconv"
)

func writeKeyPrefix(buf *bytes.Buffer, key string) {
	buf.WriteString(formatKey(key))
	buf.WriteString(" = ")
}

func writeQuotedString(buf *bytes.Buffer, s string) {
	switch first := asciiQuoteEscapeIndex(s); first {
	case -1:
		buf.WriteByte('"')
		buf.WriteString(s)
		buf.WriteByte('"')
	case quoteFallback:
		b := buf.AvailableBuffer()
		b = strconv.AppendQuote(b, s)
		buf.Write(b)
	default:
		writeQuotedASCIIString(buf, s, first)
	}
}

func asciiQuoteEscapeIndex(s string) int {
	first := -1
	for i := range len(s) {
		c := s[i]
		switch {
		case c < 0x20 || c >= 0x80:
			return quoteFallback
		case c == '"' || c == '\\':
			if first < 0 {
				first = i
			}
		}
	}
	return first
}

func writeQuotedASCIIString(buf *bytes.Buffer, s string, first int) {
	b := buf.AvailableBuffer()
	b = append(b, '"')
	start := 0
	for i := first; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			b = append(b, s[start:i]...)
			b = append(b, '\\', s[i])
			start = i + 1
		}
	}
	b = append(b, s[start:]...)
	b = append(b, '"')
	buf.Write(b)
}

func writeFloatValue(buf *bytes.Buffer, value float64, bitSize int) {
	switch {
	case math.IsInf(value, 1):
		buf.WriteString("inf")
	case math.IsInf(value, -1):
		buf.WriteString("-inf")
	case math.IsNaN(value):
		if math.Signbit(value) {
			buf.WriteString("-nan")
		} else {
			buf.WriteString("nan")
		}
	default:
		b := buf.AvailableBuffer()
		b = strconv.AppendFloat(b, value, 'g', -1, bitSize)
		if !bytes.ContainsAny(b, ".eE") {
			b = append(b, '.', '0')
		}
		buf.Write(b)
	}
}

func formatKey(key string) string {
	if key == "" {
		return strconv.Quote(key)
	}
	for _, r := range key {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return strconv.Quote(key)
	}
	return key
}
