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
	"encoding"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

var (
	_ encoding.TextMarshaler   = LocalDateTime{}
	_ encoding.TextUnmarshaler = (*LocalDateTime)(nil)
	_ encoding.TextMarshaler   = LocalDate{}
	_ encoding.TextUnmarshaler = (*LocalDate)(nil)
	_ encoding.TextMarshaler   = LocalTime{}
	_ encoding.TextUnmarshaler = (*LocalTime)(nil)
)

func TestDatetimeTextRoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source string
		want   any
	}{
		"success: local datetime without fraction": {
			source: "1979-05-27T07:32:00",
			want: LocalDateTime{
				Year: 1979, Month: 5, Day: 27,
				Hour: 7, Minute: 32, Second: 0,
				hasSecond: true,
			},
		},
		"success: local datetime with trailing fractional zeroes": {
			source: "1979-05-27T07:32:00.1200",
			want: LocalDateTime{
				Year: 1979, Month: 5, Day: 27,
				Hour: 7, Minute: 32, Second: 0,
				hasSecond:  true,
				Nanosecond: 120_000_000,
				nanoDigits: 4,
			},
		},
		"success: local date": {
			source: "1979-05-27",
			want:   LocalDate{Year: 1979, Month: 5, Day: 27},
		},
		"success: local time without fraction": {
			source: "07:32:00",
			want:   LocalTime{Hour: 7, Minute: 32, Second: 0, hasSecond: true},
		},
		"success: local datetime without seconds": {
			source: "1979-05-27T07:32",
			want: LocalDateTime{
				Year: 1979, Month: 5, Day: 27,
				Hour: 7, Minute: 32, Second: 0,
				hasSecond: false,
			},
		},
		"success: local time without seconds": {
			source: "07:32",
			want:   LocalTime{Hour: 7, Minute: 32, Second: 0, hasSecond: false},
		},
		"success: local time with nine fractional digits": {
			source: "07:32:00.123456789",
			want: LocalTime{
				Hour: 7, Minute: 32, Second: 0,
				hasSecond:  true,
				Nanosecond: 123_456_789,
				nanoDigits: 9,
			},
		},
	}

	cmpOpt := gocmp.AllowUnexported(LocalDateTime{}, LocalTime{})
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDateTime([]byte(tc.source))
			if err != nil {
				t.Fatalf("ParseDateTime(%q) error = %v", tc.source, err)
			}
			if diff := gocmp.Diff(tc.want, got, cmpOpt); diff != "" {
				t.Fatalf("ParseDateTime(%q) mismatch (-want +got):\n%s", tc.source, diff)
			}

			text, err := marshalDatetimeText(got)
			if err != nil {
				t.Fatalf("marshalDatetimeText(%T) error = %v", got, err)
			}
			if string(text) != tc.source {
				t.Fatalf("MarshalText round-trip = %q, want %q", text, tc.source)
			}
		})
	}
}

func TestDatetimeRejectsInvalidForms(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source string
	}{
		"error: one digit month":                {source: "1997-9-09"},
		"error: zero month":                     {source: "1997-00-09T09:09:09.09Z"},
		"error: zero day":                       {source: "1997-09-00T09:09:09.09Z"},
		"error: invalid leap day":               {source: "2025-02-29"},
		"error: hour too large":                 {source: "1997-09-09T30:09:09.09Z"},
		"error: minute too large":               {source: "1997-09-09T12:69:09.09Z"},
		"error: second too large":               {source: "1997-09-09T12:09:69.09Z"},
		"error: missing offset minute":          {source: "1997-09-09T09:09:09.09+09"},
		"error: trailing fractional separator":  {source: "1997-09-09T09:09:09."},
		"error: fractional precision too large": {source: "07:32:00.1234567890"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got, err := ParseDateTime([]byte(tc.source)); err == nil {
				t.Fatalf("ParseDateTime(%q) = %#v, want error", tc.source, got)
			}
		})
	}
}

func TestDatetimeOffsetDateTimeAsTime(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source     string
		wantOffset int
		wantZone   string
	}{
		"success: zulu offset":       {source: "1979-05-27T07:32:00Z", wantOffset: 0, wantZone: "UTC"},
		"success: positive offset":   {source: "1979-05-27T07:32:00+09:30", wantOffset: 9*3600 + 30*60, wantZone: "+09:30"},
		"success: negative offset":   {source: "1979-05-27T07:32:00-12:00", wantOffset: -12 * 3600, wantZone: "-12:00"},
		"success: maximum offset":    {source: "1979-05-27T07:32:00+14:00", wantOffset: 14 * 3600, wantZone: "+14:00"},
		"success: anonymous zero tz": {source: "1979-05-27T07:32:00-00:00", wantOffset: 0, wantZone: "-00:00"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDateTimeAsTime([]byte(tc.source))
			if err != nil {
				t.Fatalf("ParseDateTimeAsTime(%q) error = %v", tc.source, err)
			}
			zone, offset := got.Zone()
			if offset != tc.wantOffset || zone != tc.wantZone {
				t.Fatalf("ParseDateTimeAsTime(%q).Zone = (%q, %d), want (%q, %d)", tc.source, zone, offset, tc.wantZone, tc.wantOffset)
			}
		})
	}
}

func TestParseZoneCachesNamedOffsets(t *testing.T) {
	tests := map[string]struct {
		source string
		want   string
	}{
		"success: positive named offset": {source: "+09:30", want: "+09:30"},
		"success: positive zero offset":  {source: "+00:00", want: "+00:00"},
		"success: negative zero offset":  {source: "-00:00", want: "-00:00"},
		"success: negative named offset": {source: "-12:00", want: "-12:00"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			raw := []byte(tc.source)
			loc, err := parseZone(raw)
			if err != nil {
				t.Fatalf("parseZone(%q) warmup error = %v", tc.source, err)
			}
			if zone, _ := time.Date(2026, time.May, 20, 0, 0, 0, 0, loc).Zone(); zone != tc.want {
				t.Fatalf("parseZone(%q) warmup zone = %q, want %q", tc.source, zone, tc.want)
			}

			var got *time.Location
			allocs := testing.AllocsPerRun(1000, func() {
				got, err = parseZone(raw)
			})
			if err != nil {
				t.Fatalf("parseZone(%q) cached error = %v", tc.source, err)
			}
			if got != loc {
				t.Fatalf("parseZone(%q) cached location pointer changed", tc.source)
			}
			if zone, _ := time.Date(2026, time.May, 20, 0, 0, 0, 0, got).Zone(); zone != tc.want {
				t.Fatalf("parseZone(%q) cached zone = %q, want %q", tc.source, zone, tc.want)
			}
			if allocs != 0 {
				t.Fatalf("parseZone(%q) cached allocations = %v, want 0", tc.source, allocs)
			}
		})
	}
}

func TestDatetimeLocalIntoTimeRequiresExplicitUTC(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source string
		want   time.Time
	}{
		"success: local datetime opt in": {
			source: "1979-05-27T07:32:00.100",
			want:   time.Date(1979, time.May, 27, 7, 32, 0, 100_000_000, time.UTC),
		},
		"success: local date opt in": {
			source: "1979-05-27",
			want:   time.Date(1979, time.May, 27, 0, 0, 0, 0, time.UTC),
		},
		"success: local time opt in": {
			source: "07:32:00.100",
			want:   time.Date(0, time.January, 1, 7, 32, 0, 100_000_000, time.UTC),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDateTimeAsTime([]byte(tc.source))
			var localErr *LocalTimeIntoTimeError
			if !errors.As(err, &localErr) {
				t.Fatalf("ParseDateTimeAsTime(%q) error = %T %v, want *LocalTimeIntoTimeError", tc.source, err, err)
			}
			if string(localErr.Source) != tc.source {
				t.Fatalf("LocalTimeIntoTimeError.Source = %q, want %q", localErr.Source, tc.source)
			}

			got, err := ParseDateTimeAsTime([]byte(tc.source), WithLocalAsUTC())
			if err != nil {
				t.Fatalf("ParseDateTimeAsTime(%q, WithLocalAsUTC()) error = %v", tc.source, err)
			}
			if !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Fatalf("ParseDateTimeAsTime(%q, WithLocalAsUTC()) = %s (%s), want %s UTC", tc.source, got, got.Location(), tc.want)
			}
		})
	}
}

func TestDecoderDatetimeClassification(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source string
		want   string
	}{
		"success: offset datetime":                {source: "value = 1979-05-27T07:32:00+09:00", want: "1979-05-27T07:32:00+09:00"},
		"success: local datetime":                 {source: "value = 1979-05-27T07:32:00", want: "1979-05-27T07:32:00"},
		"success: local datetime without seconds": {source: "value = 1979-05-27T07:32", want: "1979-05-27T07:32"},
		"success: lowercase t":                    {source: "value = 1979-05-27t07:32:00", want: "1979-05-27t07:32:00"},
		"success: space separator":                {source: "value = 1979-05-27 07:32:00", want: "1979-05-27 07:32:00"},
		"success: local date":                     {source: "value = 1979-05-27", want: "1979-05-27"},
		"success: local time":                     {source: "value = 07:32:00", want: "07:32:00"},
		"success: local time without seconds":     {source: "value = 07:32", want: "07:32"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dec := NewDecoderBytes([]byte(tc.source))
			if tok, err := dec.ReadToken(); err != nil || tok.Kind != TokenKindKey {
				t.Fatalf("ReadToken key = (%v, %v), want key", tok, err)
			}
			tok, err := dec.ReadToken()
			if err != nil {
				t.Fatalf("ReadToken value error = %v", err)
			}
			if tok.Kind != TokenKindValueDatetime || string(tok.Bytes) != tc.want {
				t.Fatalf("ReadToken value = (%q, %q), want (%q, %q)", tok.Kind, tok.Bytes, TokenKindValueDatetime, tc.want)
			}
			if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
				t.Fatalf("ReadToken EOF = %v", err)
			}
		})
	}
}

func TestAppendPaddedInt(t *testing.T) {
	t.Parallel()

	boundaries := []int{0, 1, 9, 10, 99, 100, 999, 2024, 9999}

	for _, width := range []int{2, 4} {
		verb := "%02d"
		if width == 4 {
			verb = "%04d"
		}
		for _, n := range boundaries {
			name := fmt.Sprintf("success: width=%d n=%d", width, n)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				want := fmt.Sprintf(verb, n)
				got := string(appendPaddedInt(nil, n, width))
				if got != want {
					t.Fatalf("appendPaddedInt(nil, %d, %d) = %q, want %q", n, width, got, want)
				}
			})
		}
	}
}

func marshalDatetimeText(v any) ([]byte, error) {
	switch x := v.(type) {
	case time.Time:
		return x.MarshalText()
	case LocalDateTime:
		return x.MarshalText()
	case LocalDate:
		return x.MarshalText()
	case LocalTime:
		return x.MarshalText()
	default:
		return nil, errors.New("unsupported datetime value")
	}
}
