package semantic

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	maxDecimalDigits = 1000
	maxDivisionScale = 18
)

// decimal is a pure, arbitrary-precision base-10 decimal.
// It avoids all IEEE 754 floating-point nondeterminism and rounding hazards.
type decimal struct {
	negative bool
	digits   []byte // Unscaled big-endian integer digits ('0'-'9')
	scale    int    // Number of digits after the decimal point
}

func parseDecimal(text string) (decimal, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return decimal{}, fmt.Errorf("decimal string is empty")
	}

	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}

	if s == "" {
		return decimal{}, fmt.Errorf("decimal string has no digits")
	}

	dotIdx := strings.IndexByte(s, '.')
	var intPart, fracPart string
	if dotIdx >= 0 {
		intPart = s[:dotIdx]
		fracPart = s[dotIdx+1:]
		if intPart == "" && fracPart == "" {
			return decimal{}, fmt.Errorf("decimal has dot but no digits")
		}
		if strings.IndexByte(fracPart, '.') >= 0 {
			return decimal{}, fmt.Errorf("decimal has multiple dots")
		}
	} else {
		intPart = s
	}

	for i := 0; i < len(intPart); i++ {
		if intPart[i] < '0' || intPart[i] > '9' {
			return decimal{}, fmt.Errorf("invalid character %q in integer part", intPart[i])
		}
	}
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return decimal{}, fmt.Errorf("invalid character %q in fractional part", fracPart[i])
		}
	}

	allDigits := make([]byte, 0, len(intPart)+len(fracPart))
	allDigits = append(allDigits, intPart...)
	allDigits = append(allDigits, fracPart...)

	scale := len(fracPart)
	d := decimal{negative: neg, digits: allDigits, scale: scale}.normalize()
	if len(d.digits) > maxDecimalDigits {
		return decimal{}, fmt.Errorf("decimal exceeds maximum precision of %d digits", maxDecimalDigits)
	}
	return d, nil
}

func (d decimal) normalize() decimal {
	// Trim trailing zeroes in fractional part first
	digits := d.digits
	scale := d.scale
	for scale > 0 && len(digits) > 0 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
		scale--
	}

	// Trim all leading zeroes from digits
	i := 0
	for i < len(digits) && digits[i] == '0' {
		i++
	}
	digits = digits[i:]

	// If all digits were zero, or digits is empty, the value is 0 (scale 0, not negative)
	if len(digits) == 0 {
		return decimal{negative: false, digits: []byte{'0'}, scale: 0}
	}

	return decimal{negative: d.negative, digits: digits, scale: scale}
}

func (d decimal) IsZero() bool {
	return len(d.digits) == 1 && d.digits[0] == '0' && d.scale == 0
}

func (d decimal) String() string {
	d = d.normalize()
	if d.IsZero() {
		return "0"
	}

	var buf bytes.Buffer
	if d.negative {
		buf.WriteByte('-')
	}

	if d.scale == 0 {
		buf.Write(d.digits)
		return buf.String()
	}

	if len(d.digits) <= d.scale {
		// Need leading "0." and zeroes
		buf.WriteString("0.")
		leadingZeroes := d.scale - len(d.digits)
		for i := 0; i < leadingZeroes; i++ {
			buf.WriteByte('0')
		}
		buf.Write(d.digits)
	} else {
		intLen := len(d.digits) - d.scale
		buf.Write(d.digits[:intLen])
		buf.WriteByte('.')
		buf.Write(d.digits[intLen:])
	}

	return buf.String()
}

func (d decimal) Equal(other decimal) bool {
	d1 := d.normalize()
	d2 := other.normalize()
	if d1.IsZero() && d2.IsZero() {
		return true
	}
	if d1.negative != d2.negative || d1.scale != d2.scale {
		return false
	}
	return bytes.Equal(d1.digits, d2.digits)
}

func (d decimal) Less(other decimal) bool {
	d1 := d.normalize()
	d2 := other.normalize()
	if d1.Equal(d2) {
		return false
	}
	if d1.negative && !d2.negative {
		return true
	}
	if !d1.negative && d2.negative {
		return false
	}

	// Both same sign: compare absolute values
	cmp := compareAbs(d1, d2)
	if d1.negative {
		return cmp > 0
	}
	return cmp < 0
}

func compareAbs(d1, d2 decimal) int {
	// Align scales
	digits1, digits2 := alignScales(d1, d2)
	if len(digits1) < len(digits2) {
		return -1
	}
	if len(digits1) > len(digits2) {
		return 1
	}
	return bytes.Compare(digits1, digits2)
}

func alignScales(d1, d2 decimal) ([]byte, []byte) {
	maxScale := d1.scale
	if d2.scale > maxScale {
		maxScale = d2.scale
	}

	dig1 := append([]byte(nil), d1.digits...)
	for i := 0; i < maxScale-d1.scale; i++ {
		dig1 = append(dig1, '0')
	}

	dig2 := append([]byte(nil), d2.digits...)
	for i := 0; i < maxScale-d2.scale; i++ {
		dig2 = append(dig2, '0')
	}

	// Trim leading zeroes for integer length comparison
	i1 := 0
	for i1 < len(dig1)-1 && dig1[i1] == '0' {
		i1++
	}
	i2 := 0
	for i2 < len(dig2)-1 && dig2[i2] == '0' {
		i2++
	}

	return dig1[i1:], dig2[i2:]
}

func (d decimal) Add(other decimal) (decimal, error) {
	d1 := d.normalize()
	d2 := other.normalize()

	if d1.negative == d2.negative {
		maxScale := d1.scale
		if d2.scale > maxScale {
			maxScale = d2.scale
		}
		dig1, dig2 := alignDigits(d1, d2, maxScale)
		sumDigits := addDigits(dig1, dig2)
		return decimal{negative: d1.negative, digits: sumDigits, scale: maxScale}.normalize(), nil
	}

	// Different signs: subtract smaller abs from larger abs
	cmp := compareAbs(d1, d2)
	if cmp == 0 {
		return decimal{negative: false, digits: []byte{'0'}, scale: 0}, nil
	}

	maxScale := d1.scale
	if d2.scale > maxScale {
		maxScale = d2.scale
	}

	if cmp > 0 {
		dig1, dig2 := alignDigits(d1, d2, maxScale)
		diffDigits := subDigits(dig1, dig2)
		return decimal{negative: d1.negative, digits: diffDigits, scale: maxScale}.normalize(), nil
	}

	dig2, dig1 := alignDigits(d2, d1, maxScale)
	diffDigits := subDigits(dig2, dig1)
	return decimal{negative: d2.negative, digits: diffDigits, scale: maxScale}.normalize(), nil
}

func (d decimal) Sub(other decimal) (decimal, error) {
	negOther := other
	negOther.negative = !other.negative
	return d.Add(negOther)
}

func (d decimal) Mul(other decimal) (decimal, error) {
	d1 := d.normalize()
	d2 := other.normalize()
	if d1.IsZero() || d2.IsZero() {
		return decimal{negative: false, digits: []byte{'0'}, scale: 0}, nil
	}

	prodDigits := mulDigits(d1.digits, d2.digits)
	neg := d1.negative != d2.negative
	scale := d1.scale + d2.scale
	return decimal{negative: neg, digits: prodDigits, scale: scale}.normalize(), nil
}

func (d decimal) Div(other decimal) (decimal, error) {
	d1 := d.normalize()
	d2 := other.normalize()
	if d2.IsZero() {
		return decimal{}, fmt.Errorf("division by zero")
	}
	if d1.IsZero() {
		return decimal{negative: false, digits: []byte{'0'}, scale: 0}, nil
	}

	extraZeroes := maxDivisionScale + d2.scale
	dividend := append([]byte(nil), d1.digits...)
	for i := 0; i < extraZeroes; i++ {
		dividend = append(dividend, '0')
	}

	quotient := divDigits(dividend, d2.digits)
	neg := d1.negative != d2.negative
	actualScale := d1.scale + maxDivisionScale
	return decimal{negative: neg, digits: quotient, scale: actualScale}.normalize(), nil
}

func (d decimal) Abs() decimal {
	res := d
	res.negative = false
	return res
}

func (d decimal) Clamp(minVal, maxVal decimal) (decimal, error) {
	if maxVal.Less(minVal) {
		return decimal{}, fmt.Errorf("clamp min %s is greater than max %s", minVal.String(), maxVal.String())
	}
	if d.Less(minVal) {
		return minVal, nil
	}
	if maxVal.Less(d) {
		return maxVal, nil
	}
	return d, nil
}

func alignDigits(d1, d2 decimal, targetScale int) ([]byte, []byte) {
	dig1 := append([]byte(nil), d1.digits...)
	for i := 0; i < targetScale-d1.scale; i++ {
		dig1 = append(dig1, '0')
	}
	dig2 := append([]byte(nil), d2.digits...)
	for i := 0; i < targetScale-d2.scale; i++ {
		dig2 = append(dig2, '0')
	}

	maxLen := len(dig1)
	if len(dig2) > maxLen {
		maxLen = len(dig2)
	}

	pad1 := make([]byte, maxLen-len(dig1))
	for i := range pad1 {
		pad1[i] = '0'
	}
	pad2 := make([]byte, maxLen-len(dig2))
	for i := range pad2 {
		pad2[i] = '0'
	}

	return append(pad1, dig1...), append(pad2, dig2...)
}

func addDigits(a, b []byte) []byte {
	n := len(a)
	res := make([]byte, n+1)
	carry := byte(0)
	for i := n - 1; i >= 0; i-- {
		sum := (a[i] - '0') + (b[i] - '0') + carry
		res[i+1] = (sum % 10) + '0'
		carry = sum / 10
	}
	res[0] = carry + '0'
	if res[0] == '0' {
		return res[1:]
	}
	return res
}

func subDigits(a, b []byte) []byte {
	n := len(a)
	res := make([]byte, n)
	borrow := 0
	for i := n - 1; i >= 0; i-- {
		diff := int(a[i]-'0') - int(b[i]-'0') - borrow
		if diff < 0 {
			diff += 10
			borrow = 1
		} else {
			borrow = 0
		}
		res[i] = byte(diff) + '0'
	}
	return res
}

func mulDigits(a, b []byte) []byte {
	n, m := len(a), len(b)
	res := make([]int, n+m)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			mul := int(a[i]-'0') * int(b[j]-'0')
			p1, p2 := i+j, i+j+1
			sum := mul + res[p2]
			res[p2] = sum % 10
			res[p1] += sum / 10
		}
	}
	out := make([]byte, 0, len(res))
	started := false
	for _, v := range res {
		if v != 0 || started {
			started = true
			out = append(out, byte(v)+'0')
		}
	}
	if len(out) == 0 {
		return []byte{'0'}
	}
	return out
}

func divDigits(dividend, divisor []byte) []byte {
	quotient := make([]byte, 0, len(dividend))
	remainder := []byte{'0'}

	for _, b := range dividend {
		if len(remainder) == 1 && remainder[0] == '0' {
			remainder = []byte{b}
		} else {
			remainder = append(remainder, b)
		}

		qDigit := byte(0)
		for testQ := byte(9); testQ >= 1; testQ-- {
			prod := mulSingleDigit(divisor, testQ)
			if compareDigitSlices(remainder, prod) >= 0 {
				qDigit = testQ
				remainder = subDigitSlices(remainder, prod)
				break
			}
		}
		quotient = append(quotient, qDigit+'0')
	}

	// Trim leading zeroes in quotient
	i := 0
	for i < len(quotient)-1 && quotient[i] == '0' {
		i++
	}
	return quotient[i:]
}

func mulSingleDigit(a []byte, d byte) []byte {
	if d == 0 {
		return []byte{'0'}
	}
	n := len(a)
	res := make([]byte, n+1)
	carry := byte(0)
	dVal := d // e.g. 1..9
	for i := n - 1; i >= 0; i-- {
		prod := (a[i]-'0')*dVal + carry
		res[i+1] = (prod % 10) + '0'
		carry = prod / 10
	}
	res[0] = carry + '0'
	i := 0
	for i < len(res)-1 && res[i] == '0' {
		i++
	}
	return res[i:]
}

func compareDigitSlices(a, b []byte) int {
	// Assume both slices have no redundant leading zeroes
	iA := 0
	for iA < len(a)-1 && a[iA] == '0' {
		iA++
	}
	iB := 0
	for iB < len(b)-1 && b[iB] == '0' {
		iB++
	}
	sA := a[iA:]
	sB := b[iB:]
	if len(sA) != len(sB) {
		if len(sA) > len(sB) {
			return 1
		}
		return -1
	}
	return bytes.Compare(sA, sB)
}

func subDigitSlices(a, b []byte) []byte {
	n := len(a)
	m := len(b)
	bPad := make([]byte, n)
	for i := 0; i < n-m; i++ {
		bPad[i] = '0'
	}
	copy(bPad[n-m:], b)

	res := make([]byte, n)
	borrow := 0
	for i := n - 1; i >= 0; i-- {
		diff := int(a[i]-'0') - int(bPad[i]-'0') - borrow
		if diff < 0 {
			diff += 10
			borrow = 1
		} else {
			borrow = 0
		}
		res[i] = byte(diff) + '0'
	}
	i := 0
	for i < len(res)-1 && res[i] == '0' {
		i++
	}
	return res[i:]
}
