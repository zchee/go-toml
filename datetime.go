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
	"fmt"
	"strconv"
	"time"
)

// LocalDateTime is a TOML local date-time without offset information.
//
// The unexported fractional-second precision preserves whether the source used
// no fraction, a short fraction, or explicit trailing zeroes.
type LocalDateTime struct {
	Year, Month, Day     int
	Hour, Minute, Second int
	Nanosecond           int
	hasSecond            bool

	nanoDigits int8
}

var (
	_ encoding.TextMarshaler   = (*LocalDateTime)(nil)
	_ encoding.TextUnmarshaler = (*LocalDateTime)(nil)
)

// String returns the TOML representation of dt.
func (dt LocalDateTime) String() string {
	text, err := dt.MarshalText()
	if err != nil {
		return ""
	}
	return string(text)
}

// MarshalText implements [encoding.TextMarshaler].
func (dt LocalDateTime) MarshalText() ([]byte, error) {
	if err := validateDate(dt.Year, dt.Month, dt.Day); err != nil {
		return nil, err
	}
	if err := validateLocalClock(dt.Hour, dt.Minute, dt.Second, dt.Nanosecond, dt.nanoDigits); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, len("0000-00-00T00:00:00.000000000"))
	buf = appendDate(buf, dt.Year, dt.Month, dt.Day)
	buf = append(buf, 'T')
	buf = appendTime(buf, dt.Hour, dt.Minute, dt.Second, dt.Nanosecond, dt.nanoDigits, dt.hasSecond)
	return buf, nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (dt *LocalDateTime) UnmarshalText(text []byte) error {
	v, err := parseLocalDateTime(text)
	if err != nil {
		return err
	}
	*dt = v
	return nil
}

// LocalDate is a TOML local date without time or offset information.
type LocalDate struct {
	Year, Month, Day int
}

var (
	_ encoding.TextMarshaler   = (*LocalDate)(nil)
	_ encoding.TextUnmarshaler = (*LocalDate)(nil)
)

// String returns the TOML representation of d.
func (d LocalDate) String() string {
	text, err := d.MarshalText()
	if err != nil {
		return ""
	}
	return string(text)
}

// MarshalText implements [encoding.TextMarshaler].
func (d LocalDate) MarshalText() ([]byte, error) {
	if err := validateDate(d.Year, d.Month, d.Day); err != nil {
		return nil, err
	}
	return appendDate(make([]byte, 0, len("0000-00-00")), d.Year, d.Month, d.Day), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (d *LocalDate) UnmarshalText(text []byte) error {
	v, err := parseLocalDate(text)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// LocalTime is a TOML local time without date or offset information.
//
// The unexported fractional-second precision preserves whether the source used
// no fraction, a short fraction, or explicit trailing zeroes.
type LocalTime struct {
	Hour, Minute, Second int
	Nanosecond           int
	hasSecond            bool

	nanoDigits int8
}

var (
	_ encoding.TextMarshaler   = (*LocalTime)(nil)
	_ encoding.TextUnmarshaler = (*LocalTime)(nil)
)

// String returns the TOML representation of lt.
func (lt LocalTime) String() string {
	text, err := lt.MarshalText()
	if err != nil {
		return ""
	}
	return string(text)
}

// MarshalText implements [encoding.TextMarshaler].
func (lt LocalTime) MarshalText() ([]byte, error) {
	if err := validateLocalClock(lt.Hour, lt.Minute, lt.Second, lt.Nanosecond, lt.nanoDigits); err != nil {
		return nil, err
	}
	return appendTime(make([]byte, 0, len("00:00:00.000000000")), lt.Hour, lt.Minute, lt.Second, lt.Nanosecond, lt.nanoDigits, lt.hasSecond), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (lt *LocalTime) UnmarshalText(text []byte) error {
	v, err := parseLocalTime(text)
	if err != nil {
		return err
	}
	*lt = v
	return nil
}

// ParseLocalDateTime parses a TOML local date-time.
func ParseLocalDateTime(text []byte) (LocalDateTime, error) {
	v, err := parseLocalDateTime(text)
	if err != nil {
		return LocalDateTime{}, err
	}
	return v, nil
}

// ParseLocalDate parses a TOML local date.
func ParseLocalDate(text []byte) (LocalDate, error) {
	v, err := parseLocalDate(text)
	if err != nil {
		return LocalDate{}, err
	}
	return v, nil
}

// ParseLocalTime parses a TOML local time.
func ParseLocalTime(text []byte) (LocalTime, error) {
	v, err := parseLocalTime(text)
	if err != nil {
		return LocalTime{}, err
	}
	return v, nil
}

// ParseOffsetDateTime parses a TOML offset date-time into time.Time.
func ParseOffsetDateTime(text []byte) (time.Time, error) {
	v, err := parseOffsetDateTime(text)
	if err != nil {
		return time.Time{}, err
	}
	return v, nil
}

// ParseDateTime parses one TOML datetime value.
//
// It returns time.Time for offset date-times, LocalDateTime for local
// date-times, LocalDate for local dates, and LocalTime for local times.
func ParseDateTime(text []byte) (any, error) {
	v, _, err := parseDateTimeValue(text)
	return v, err
}

// ParseDateTimeAsTime parses a TOML datetime value into time.Time.
//
// Offset date-times preserve their numeric offset using time.FixedZone. Local
// TOML forms return *LocalTimeIntoTimeError unless WithLocalAsUTC is supplied.
func ParseDateTimeAsTime(text []byte, opts ...Option) (time.Time, error) {
	return parseDateTimeAsTime(text, [2]int{0, len(text)}, opts...)
}

func appendDate(buf []byte, year, month, day int) []byte {
	buf = appendPaddedInt(buf, year, 4)
	buf = append(buf, '-')
	buf = appendPaddedInt(buf, month, 2)
	buf = append(buf, '-')
	buf = appendPaddedInt(buf, day, 2)
	return buf
}

func appendTime(buf []byte, hour, minute, second, nanos int, nanoDigits int8, hasSecond bool) []byte {
	buf = appendPaddedInt(buf, hour, 2)
	buf = append(buf, ':')
	buf = appendPaddedInt(buf, minute, 2)
	if hasSecond {
		buf = append(buf, ':')
		buf = appendPaddedInt(buf, second, 2)
	}
	if hasSecond && nanoDigits > 0 {
		buf = append(buf, '.')
		frac := strconv.Itoa(nanos + 1_000_000_000)
		buf = append(buf, frac[1:1+int(nanoDigits)]...)
	}
	return buf
}

func appendPaddedInt(buf []byte, n, width int) []byte {
	if width == 4 {
		return fmt.Appendf(buf, "%04d", n)
	}
	return fmt.Appendf(buf, "%02d", n)
}
