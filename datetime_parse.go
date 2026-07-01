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
	"fmt"
	"slices"
	"sync/atomic"
	"time"
)

type dateTimeKind uint8

const (
	dateTimeKindInvalid dateTimeKind = iota
	dateTimeKindOffset
	dateTimeKindLocalDateTime
	dateTimeKindLocalDate
	dateTimeKindLocalTime
)

var fixedOffsetZoneCache [2][24][60]atomic.Pointer[time.Location]

// parseDateTimeValue parses raw as the most specific TOML datetime form it matches.
func parseDateTimeValue(raw []byte) (any, dateTimeKind, error) {
	switch dateTimeShape(raw) {
	case dateTimeKindOffset:
		t, err := parseOffsetDateTime(raw)
		if err != nil {
			return nil, dateTimeKindInvalid, invalidDateTimeError(raw)
		}
		return t, dateTimeKindOffset, nil
	case dateTimeKindLocalDateTime:
		dt, err := parseLocalDateTime(raw)
		if err != nil {
			return nil, dateTimeKindInvalid, invalidDateTimeError(raw)
		}
		return dt, dateTimeKindLocalDateTime, nil
	case dateTimeKindLocalDate:
		d, err := parseLocalDate(raw)
		if err != nil {
			return nil, dateTimeKindInvalid, invalidDateTimeError(raw)
		}
		return d, dateTimeKindLocalDate, nil
	case dateTimeKindLocalTime:
		lt, err := parseLocalTime(raw)
		if err != nil {
			return nil, dateTimeKindInvalid, invalidDateTimeError(raw)
		}
		return lt, dateTimeKindLocalTime, nil
	default:
		return nil, dateTimeKindInvalid, invalidDateTimeError(raw)
	}
}

func dateTimeShape(raw []byte) dateTimeKind {
	if hasDateShape(raw) {
		return dateShape(raw)
	}
	if hasTimeShape(raw) {
		return dateTimeKindLocalTime
	}
	return dateTimeKindInvalid
}

// dateShape classifies a value already known to begin with a date (date-only,
// local datetime, or offset datetime).
func dateShape(raw []byte) dateTimeKind {
	if len(raw) == len("0000-00-00") {
		return dateTimeKindLocalDate
	}
	if len(raw) > len("0000-00-00") && isDateTimeSeparator(raw[len("0000-00-00")]) {
		if hasDateTimeOffsetSuffix(raw[len("0000-00-00")+1:]) {
			return dateTimeKindOffset
		}
		return dateTimeKindLocalDateTime
	}
	return dateTimeKindInvalid
}

func hasDateTimeOffsetSuffix(raw []byte) bool {
	for i := len("00:00"); i < len(raw); i++ {
		switch raw[i] {
		case 'Z', 'z', '+', '-':
			return true
		}
	}
	return false
}

func invalidDateTimeError(raw []byte) error {
	return fmt.Errorf("toml: invalid datetime %q", raw)
}

func parseDateTimeAsTime(raw []byte, span [2]int, opts ...Option) (time.Time, error) {
	v, kind, err := parseDateTimeValue(raw)
	if err != nil {
		return time.Time{}, err
	}
	if kind == dateTimeKindOffset {
		return v.(time.Time), nil
	}

	cfg := &Decoder{}
	for _, opt := range opts {
		opt(cfg)
	}
	if !cfg.localAsUTC {
		return time.Time{}, &LocalTimeIntoTimeError{
			Kind:   TokenKindValueDatetime,
			Source: slices.Clone(raw),
			Span:   span,
		}
	}

	switch x := v.(type) {
	case LocalDateTime:
		return time.Date(
			x.Year,
			time.Month(x.Month),
			x.Day,
			x.Hour, x.Minute, x.Second, x.Nanosecond,
			time.UTC,
		), nil

	case LocalDate:
		return time.Date(
			x.Year,
			time.Month(x.Month),
			x.Day,
			0, 0, 0, 0,
			time.UTC,
		), nil

	case LocalTime:
		return time.Date(
			0,
			time.January,
			1,
			x.Hour, x.Minute, x.Second, x.Nanosecond,
			time.UTC,
		), nil

	default:
		return time.Time{}, fmt.Errorf("toml: unsupported datetime value %T", v)
	}
}

func parseOffsetDateTime(raw []byte) (time.Time, error) {
	date, timeStart, err := parseDatePrefix(raw)
	if err != nil {
		return time.Time{}, err
	}
	if timeStart >= len(raw) || !isDateTimeSeparator(raw[timeStart]) {
		return time.Time{}, fmt.Errorf("toml: missing datetime separator")
	}
	clock, next, err := parseTimePrefix(raw[timeStart+1:])
	if err != nil {
		return time.Time{}, err
	}
	off := timeStart + 1 + next
	if off >= len(raw) {
		return time.Time{}, fmt.Errorf("toml: missing datetime offset")
	}
	loc, err := parseZone(raw[off:])
	if err != nil {
		return time.Time{}, err
	}
	t := time.Date(
		date.Year,
		time.Month(date.Month),
		date.Day,
		clock.Hour, clock.Minute, clock.Second, clock.Nanosecond,
		loc,
	)
	return t, nil
}

func parseLocalDateTime(raw []byte) (LocalDateTime, error) {
	date, timeStart, err := parseDatePrefix(raw)
	if err != nil {
		return LocalDateTime{}, err
	}
	if timeStart >= len(raw) || !isDateTimeSeparator(raw[timeStart]) {
		return LocalDateTime{}, fmt.Errorf("toml: missing datetime separator")
	}
	clock, next, err := parseTimePrefix(raw[timeStart+1:])
	if err != nil {
		return LocalDateTime{}, err
	}
	if timeStart+1+next != len(raw) {
		return LocalDateTime{}, fmt.Errorf("toml: unexpected trailing datetime data")
	}
	return LocalDateTime{
		Year:       date.Year,
		Month:      date.Month,
		Day:        date.Day,
		Hour:       clock.Hour,
		Minute:     clock.Minute,
		Second:     clock.Second,
		Nanosecond: clock.Nanosecond,
		hasSecond:  clock.hasSecond,
		nanoDigits: clock.nanoDigits,
	}, nil
}

func parseLocalDate(raw []byte) (LocalDate, error) {
	date, next, err := parseDatePrefix(raw)
	if err != nil {
		return LocalDate{}, err
	}
	if next != len(raw) {
		return LocalDate{}, fmt.Errorf("toml: unexpected trailing date data")
	}
	return date, nil
}

func parseLocalTime(raw []byte) (LocalTime, error) {
	clock, next, err := parseTimePrefix(raw)
	if err != nil {
		return LocalTime{}, err
	}
	if next != len(raw) {
		return LocalTime{}, fmt.Errorf("toml: unexpected trailing time data")
	}
	return clock, nil
}

func parseDatePrefix(raw []byte) (LocalDate, int, error) {
	if len(raw) < len("0000-00-00") {
		return LocalDate{}, 0, fmt.Errorf("toml: short date")
	}
	if raw[4] != '-' || raw[7] != '-' {
		return LocalDate{}, 0, fmt.Errorf("toml: malformed date separators")
	}

	year, ok := parseFixedDigits(raw[0:4])
	if !ok {
		return LocalDate{}, 0, fmt.Errorf("toml: malformed year")
	}
	month, ok := parseFixedDigits(raw[5:7])
	if !ok {
		return LocalDate{}, 0, fmt.Errorf("toml: malformed month")
	}
	day, ok := parseFixedDigits(raw[8:10])
	if !ok {
		return LocalDate{}, 0, fmt.Errorf("toml: malformed day")
	}
	if err := validateDate(year, month, day); err != nil {
		return LocalDate{}, 0, err
	}

	date := LocalDate{
		Year:  year,
		Month: month,
		Day:   day,
	}
	return date, len("0000-00-00"), nil
}

func parseTimePrefix(raw []byte) (LocalTime, int, error) {
	if len(raw) < len("00:00") {
		return LocalTime{}, 0, fmt.Errorf("toml: short time")
	}
	if raw[2] != ':' {
		return LocalTime{}, 0, fmt.Errorf("toml: malformed time separators")
	}

	hour, ok := parseFixedDigits(raw[0:2])
	if !ok {
		return LocalTime{}, 0, fmt.Errorf("toml: malformed hour")
	}
	minute, ok := parseFixedDigits(raw[3:5])
	if !ok {
		return LocalTime{}, 0, fmt.Errorf("toml: malformed minute")
	}

	second, nanos, nanoDigits, next, err := parseClockSubseconds(raw, len("00:00"))
	if err != nil {
		return LocalTime{}, 0, err
	}
	if err := validateLocalClock(hour, minute, second, nanos, nanoDigits); err != nil {
		return LocalTime{}, 0, err
	}

	clock := LocalTime{
		Hour:       hour,
		Minute:     minute,
		Second:     second,
		Nanosecond: nanos,
		hasSecond:  next > len("00:00"),
		nanoDigits: nanoDigits,
	}
	return clock, next, nil
}

// parseClockSubseconds parses the optional ":SS[.fraction]" suffix of a TOML
// time beginning at off. It returns the parsed seconds, nanoseconds, fractional
// digit count, and the index just past the consumed suffix.
func parseClockSubseconds(raw []byte, off int) (second, nanos int, nanoDigits int8, next int, err error) {
	next = off
	if next >= len(raw) || raw[next] != ':' {
		return 0, 0, 0, next, nil
	}
	next++
	if len(raw)-next < len("00") {
		return 0, 0, 0, next, fmt.Errorf("toml: short time")
	}
	sec, ok := parseFixedDigits(raw[next : next+2])
	if !ok {
		return 0, 0, 0, next, fmt.Errorf("toml: malformed second")
	}
	second = sec
	next += 2
	if next >= len(raw) || raw[next] != '.' {
		return second, 0, 0, next, nil
	}
	next++
	fracStart := next
	for next < len(raw) && raw[next] >= '0' && raw[next] <= '9' {
		next++
	}
	if next == fracStart {
		return 0, 0, 0, next, fmt.Errorf("toml: empty fractional second")
	}
	if next-fracStart > 9 {
		return 0, 0, 0, next, fmt.Errorf("toml: fractional second precision exceeds nanoseconds")
	}
	nanoDigits = int8(next - fracStart) //nolint:gosec // G115: guarded to [1,9] by the empty and >9 checks above.
	for i := fracStart; i < next; i++ {
		nanos = nanos*10 + int(raw[i]-'0')
	}
	for range 9 - int(nanoDigits) {
		nanos *= 10
	}
	return second, nanos, nanoDigits, next, nil
}

func parseZone(raw []byte) (*time.Location, error) {
	if len(raw) == 1 && (raw[0] == 'Z' || raw[0] == 'z') {
		return time.UTC, nil
	}
	if len(raw) != len("+00:00") || (raw[0] != '+' && raw[0] != '-') || raw[3] != ':' {
		return nil, fmt.Errorf("toml: malformed datetime offset")
	}

	hour, ok := parseFixedDigits(raw[1:3])
	if !ok {
		return nil, fmt.Errorf("toml: malformed offset hour")
	}
	minute, ok := parseFixedDigits(raw[4:6])
	if !ok {
		return nil, fmt.Errorf("toml: malformed offset minute")
	}
	if hour > 23 || minute > 59 {
		return nil, fmt.Errorf("toml: offset out of range")
	}

	seconds := hour*3600 + minute*60
	if raw[0] == '-' {
		seconds = -seconds
	}
	return fixedOffsetZone(raw, seconds, hour, minute), nil
}

func fixedOffsetZone(raw []byte, seconds, hour, minute int) *time.Location {
	sign := 0
	if raw[0] == '-' {
		sign = 1
	}
	slot := &fixedOffsetZoneCache[sign][hour][minute]
	if loc := slot.Load(); loc != nil {
		return loc
	}
	loc := time.FixedZone(string(raw), seconds)
	if slot.CompareAndSwap(nil, loc) {
		return loc
	}
	return slot.Load()
}

func parseFixedDigits(raw []byte) (int, bool) {
	v := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		v = v*10 + int(ch-'0')
	}
	return v, true
}

func validateDate(year, month, day int) error {
	if year < 0 || year > 9999 {
		return fmt.Errorf("toml: year out of range")
	}
	if month < 1 || month > 12 {
		return fmt.Errorf("toml: month out of range")
	}
	if day < 1 || day > daysInMonth(year, month) {
		return fmt.Errorf("toml: day out of range")
	}
	return nil
}

// validateLocalClock checks local time ranges and fractional precision consistency.
func validateLocalClock(hour, minute, second, nanos int, nanoDigits int8) error {
	if hour < 0 || hour > 23 {
		return fmt.Errorf("toml: hour out of range")
	}
	if minute < 0 || minute > 59 {
		return fmt.Errorf("toml: minute out of range")
	}
	if second < 0 || second > 59 {
		return fmt.Errorf("toml: second out of range")
	}
	if nanos < 0 || nanos >= int(time.Second) {
		return fmt.Errorf("toml: nanosecond out of range")
	}
	if nanoDigits < 0 || nanoDigits > 9 {
		return fmt.Errorf("toml: fractional precision out of range")
	}
	if nanoDigits == 0 && nanos != 0 {
		return fmt.Errorf("toml: nanosecond requires fractional precision")
	}
	return nil
}

// daysInMonth returns the number of days in the given month of the given year.
func daysInMonth(year, month int) int {
	switch time.Month(month) {
	case time.January, time.March, time.May, time.July, time.August, time.October, time.December:
		return 31

	case time.April, time.June, time.September, time.November:
		return 30

	case time.February:
		if isLeapYear(year) {
			return 29
		}
		return 28

	default:
		return 0
	}
}

// isLeapYear reports whether the given year is a leap year.
func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// isDateTimeSeparator reports whether the given byte is a valid date-time separator.
func isDateTimeSeparator(ch byte) bool {
	return ch == 'T' || ch == 't' || ch == ' '
}
