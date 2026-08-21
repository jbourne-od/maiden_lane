package semantic

import (
	"fmt"
	"strconv"
	"strings"
)

// timestamp is a pure, immutable UTC instant in time.
// It uses nanoseconds since the Unix epoch (1970-01-01T00:00:00Z).
type timestamp struct {
	unixNano int64
}

const (
	minTimestampSec     = -9223372036
	maxTimestampSec     = 9223372036
	maxFracNanoAtMaxSec = 854775807
)

// date is a pure, immutable calendar date (ISO-8601 YYYY-MM-DD).
// It uses whole days since the Unix epoch (1970-01-01).
type date struct {
	daysSinceEpoch int64
}

func isLeapYear(year int64) bool {
	return (year%4 == 0 && year%100 != 0) || (year%400 == 0)
}

func daysInMonth(year, month int64) int64 {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeapYear(year) {
			return 29
		}
		return 28
	default:
		return 0
	}
}

// daysSinceEpochFromCivil computes days since 1970-01-01 using Howard Hinnant's algorithm.
func daysSinceEpochFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	monthOffset := m - 3
	if m <= 2 {
		monthOffset = m + 9
	}
	doy := (153*monthOffset+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// civilFromDaysSinceEpoch computes (year, month, day) from days since 1970-01-01.
func civilFromDaysSinceEpoch(z int64) (year, month, day int64) {
	z += 719468
	era := z / 146097
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

// dayOfWeekFromDays returns ISO weekday (1 = Monday, ..., 7 = Sunday).
// 1970-01-01 was a Thursday (ISO weekday 4).
func dayOfWeekFromDays(days int64) int64 {
	rem := (days + 3) % 7
	if rem < 0 {
		rem += 7
	}
	return rem + 1
}

func parseTimestamp(s string) (timestamp, error) {
	text := strings.TrimSpace(s)
	if text == "" {
		return timestamp{}, fmt.Errorf("timestamp string is empty")
	}

	// Must contain 'T'
	tIdx := strings.IndexByte(text, 'T')
	if tIdx < 0 {
		return timestamp{}, fmt.Errorf("timestamp missing 'T' delimiter")
	}

	dateStr := text[:tIdx]
	timeStr := text[tIdx+1:]

	// Parse date portion
	d, err := parseDate(dateStr)
	if err != nil {
		return timestamp{}, fmt.Errorf("invalid date in timestamp: %w", err)
	}

	// Parse time and timezone
	// Must end in 'Z', '+00:00', or '-00:00'
	if strings.HasSuffix(timeStr, "Z") {
		timeStr = strings.TrimSuffix(timeStr, "Z")
	} else if strings.HasSuffix(timeStr, "+00:00") {
		timeStr = strings.TrimSuffix(timeStr, "+00:00")
	} else if strings.HasSuffix(timeStr, "-00:00") {
		timeStr = strings.TrimSuffix(timeStr, "-00:00")
	} else {
		return timestamp{}, fmt.Errorf("invalid RFC 3339 timestamp %q (must use UTC: Z, +00:00, or -00:00)", s)
	}

	timeParts := strings.Split(timeStr, ":")
	if len(timeParts) != 3 {
		return timestamp{}, fmt.Errorf("invalid time part in timestamp %q", timeStr)
	}

	if len(timeParts[0]) != 2 || len(timeParts[1]) != 2 {
		return timestamp{}, fmt.Errorf("invalid hour/minute format in timestamp %q", timeStr)
	}

	hour, err := strconv.ParseInt(timeParts[0], 10, 64)
	if err != nil || hour < 0 || hour > 23 {
		return timestamp{}, fmt.Errorf("invalid hour %q in timestamp", timeParts[0])
	}

	min, err := strconv.ParseInt(timeParts[1], 10, 64)
	if err != nil || min < 0 || min > 59 {
		return timestamp{}, fmt.Errorf("invalid minute %q in timestamp", timeParts[1])
	}

	secStr := timeParts[2]
	var sec int64
	var fracNano int64

	if dotIdx := strings.IndexByte(secStr, '.'); dotIdx >= 0 {
		intSec := secStr[:dotIdx]
		if len(intSec) != 2 {
			return timestamp{}, fmt.Errorf("invalid seconds %q in timestamp", intSec)
		}
		sec, err = strconv.ParseInt(intSec, 10, 64)
		if err != nil || sec < 0 || sec > 59 {
			return timestamp{}, fmt.Errorf("invalid seconds %q in timestamp", intSec)
		}

		fracPart := secStr[dotIdx+1:]
		if len(fracPart) == 0 || len(fracPart) > 9 {
			return timestamp{}, fmt.Errorf("invalid fractional seconds %q in timestamp", fracPart)
		}
		paddedFrac := fracPart + strings.Repeat("0", 9-len(fracPart))
		fracNano, err = strconv.ParseInt(paddedFrac, 10, 64)
		if err != nil || fracNano < 0 {
			return timestamp{}, fmt.Errorf("invalid fractional nanoseconds %q in timestamp", fracPart)
		}
	} else {
		if len(secStr) != 2 {
			return timestamp{}, fmt.Errorf("invalid seconds %q in timestamp", secStr)
		}
		sec, err = strconv.ParseInt(secStr, 10, 64)
		if err != nil || sec < 0 || sec > 59 {
			return timestamp{}, fmt.Errorf("invalid seconds %q in timestamp", secStr)
		}
	}

	daySec := hour*3600 + min*60 + sec
	if d.daysSinceEpoch < -106752 || d.daysSinceEpoch > 106751 {
		return timestamp{}, fmt.Errorf("timestamp %q is outside representable range", s)
	}
	totalSec := d.daysSinceEpoch*86400 + daySec
	if totalSec < minTimestampSec || totalSec > maxTimestampSec ||
		(totalSec == maxTimestampSec && fracNano > maxFracNanoAtMaxSec) {
		return timestamp{}, fmt.Errorf("timestamp %q is outside representable range", s)
	}
	totalNano := totalSec*1e9 + fracNano

	return timestamp{unixNano: totalNano}, nil
}

func (t timestamp) String() string {
	totalSec := t.unixNano / 1e9
	fracNano := t.unixNano % 1e9
	if fracNano < 0 {
		fracNano += 1e9
		totalSec--
	}

	days := totalSec / 86400
	daySec := totalSec % 86400
	if daySec < 0 {
		daySec += 86400
		days--
	}

	y, m, d := civilFromDaysSinceEpoch(days)
	hour := daySec / 3600
	min := (daySec % 3600) / 60
	sec := daySec % 60

	if fracNano == 0 {
		return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02dZ", y, m, d, hour, min, sec)
	}

	fracStr := fmt.Sprintf("%09d", fracNano)
	fracStr = strings.TrimRight(fracStr, "0")
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%sZ", y, m, d, hour, min, sec, fracStr)
}

func (t timestamp) Equal(other timestamp) bool {
	return t.unixNano == other.unixNano
}

func (t timestamp) Less(other timestamp) bool {
	return t.unixNano < other.unixNano
}

func (t timestamp) AddDuration(seconds int64) (timestamp, error) {
	totalSec := t.unixNano / 1e9
	fracNano := t.unixNano % 1e9
	if fracNano < 0 {
		fracNano += 1e9
		totalSec--
	}
	newSec, err := addInt64(totalSec, seconds)
	if err != nil || newSec < minTimestampSec || newSec > maxTimestampSec ||
		(newSec == maxTimestampSec && fracNano > maxFracNanoAtMaxSec) {
		return timestamp{}, fmt.Errorf("timestamp addition overflows representable timestamp range")
	}
	return timestamp{unixNano: newSec*1e9 + fracNano}, nil
}

func (t timestamp) Diff(other timestamp) (int64, error) {
	tSec := t.unixNano / 1e9
	tFrac := t.unixNano % 1e9
	if tFrac < 0 {
		tFrac += 1e9
		tSec--
	}

	oSec := other.unixNano / 1e9
	oFrac := other.unixNano % 1e9
	if oFrac < 0 {
		oFrac += 1e9
		oSec--
	}

	dSec := tSec - oSec
	dFrac := tFrac - oFrac

	if dSec > 0 {
		if dFrac < 0 {
			dSec--
		}
	} else if dSec < 0 {
		if dFrac > 0 {
			dSec++
		}
	}

	return dSec, nil
}

func (t timestamp) DateExtract(unit string) (int64, error) {
	totalSec := t.unixNano / 1e9
	if t.unixNano < 0 && t.unixNano%1e9 != 0 {
		totalSec--
	}

	days := totalSec / 86400
	daySec := totalSec % 86400
	if daySec < 0 {
		daySec += 86400
		days--
	}

	y, m, d := civilFromDaysSinceEpoch(days)
	hour := daySec / 3600
	min := (daySec % 3600) / 60
	sec := daySec % 60

	switch strings.ToLower(unit) {
	case "year":
		return y, nil
	case "month":
		return m, nil
	case "day":
		return d, nil
	case "hour":
		return hour, nil
	case "minute":
		return min, nil
	case "second":
		return sec, nil
	case "day_of_week":
		return dayOfWeekFromDays(days), nil
	default:
		return 0, fmt.Errorf("unknown date extract unit %q", unit)
	}
}

func parseDate(s string) (date, error) {
	text := strings.TrimSpace(s)
	parts := strings.Split(text, "-")
	if len(parts) != 3 {
		return date{}, fmt.Errorf("date must use YYYY-MM-DD format")
	}

	if len(parts[0]) != 4 || len(parts[1]) != 2 || len(parts[2]) != 2 {
		return date{}, fmt.Errorf("date components must be 4, 2, and 2 digits")
	}

	year, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || year < 1 || year > 9999 {
		return date{}, fmt.Errorf("invalid year %q in date", parts[0])
	}

	month, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || month < 1 || month > 12 {
		return date{}, fmt.Errorf("invalid month %q in date", parts[1])
	}

	maxDays := daysInMonth(year, month)
	day, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || day < 1 || day > maxDays {
		return date{}, fmt.Errorf("invalid day %q for month %02d/%04d in date", parts[2], month, year)
	}

	days := daysSinceEpochFromCivil(year, month, day)
	return date{daysSinceEpoch: days}, nil
}

func (d date) String() string {
	y, m, day := civilFromDaysSinceEpoch(d.daysSinceEpoch)
	return fmt.Sprintf("%04d-%02d-%02d", y, m, day)
}

func (d date) Equal(other date) bool {
	return d.daysSinceEpoch == other.daysSinceEpoch
}

func (d date) Less(other date) bool {
	return d.daysSinceEpoch < other.daysSinceEpoch
}

func (d date) DateExtract(unit string) (int64, error) {
	y, m, day := civilFromDaysSinceEpoch(d.daysSinceEpoch)
	switch strings.ToLower(unit) {
	case "year":
		return y, nil
	case "month":
		return m, nil
	case "day":
		return day, nil
	case "day_of_week":
		return dayOfWeekFromDays(d.daysSinceEpoch), nil
	default:
		return 0, fmt.Errorf("unknown date extract unit %q for date", unit)
	}
}
