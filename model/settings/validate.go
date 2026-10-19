package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Validation errors, worded for the owner.
var (
	ErrNotBool          = errors.New("must be true or false")
	ErrNotNumber        = errors.New("must be a number")
	ErrNotWholeNumber   = errors.New("must be a whole number")
	ErrNotString        = errors.New("must be text")
	ErrNotList          = errors.New("must be a list of names")
	ErrEmptyList        = errors.New("must name at least one")
	ErrRepeatedListItem = errors.New("must not name one twice")
	ErrUnknownKey       = errors.New("unknown setting")
	ErrTrailingData     = errors.New("must be a single value")
)

// Host validation errors.
var (
	ErrHostEmpty     = errors.New("must not be empty")
	ErrHostTooLong   = errors.New("must be at most 253 characters")
	ErrHostIP        = errors.New("must be a hostname, not an IP address")
	ErrHostLabels    = errors.New("must have at least two labels, like example.com")
	ErrHostCharacter = errors.New("must be a hostname only (letters, digits, hyphens and dots; no scheme, port, path or user)")
	ErrHostLabel     = errors.New("has a label that is empty, longer than 63 characters, or starts or ends with a hyphen")
)

// Host limits (RFC 1035).
const (
	hostMaxLength      = 253
	hostLabelMaxLength = 63
	hostMinLabels      = 2
	hostSeparator      = "."
	hostLabelHyphen    = "-"
)

// Validate parses a stored or requested JSON value and checks it against the
// setting's type, range and hard ceiling. It returns the typed value: bool,
// int, float64, string or []string.
func (s Setting) Validate(raw json.RawMessage) (any, error) {
	var result any
	var err error

	switch s.Type {
	case TypeBool:
		result, err = decodeBool(raw)
	case TypeInt:
		result, err = s.validateInt(raw)
	case TypeDecimal:
		result, err = s.validateDecimal(raw)
	case TypeEnum:
		result, err = s.validateEnum(raw)
	case TypeHost:
		result, err = validateHostValue(raw)
	case TypeOrderedList:
		result, err = s.validateOrderedList(raw)
	default:
		err = fmt.Errorf("unsupported type %q", s.Type)
	}

	if err != nil {
		result = nil
		err = fmt.Errorf("%s %w", s.Key, err)
	}

	return result, err
}

// decodeStrict decodes exactly one JSON value, numbers as json.Number.
func decodeStrict(raw json.RawMessage) (any, error) {
	var result any
	reader := bytes.NewReader(raw)
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	err := decoder.Decode(&result)

	if (err == nil) && (decoder.More() == true) {
		err = ErrTrailingData
	}

	return result, err
}

// decodeBool decodes a JSON boolean.
func decodeBool(raw json.RawMessage) (any, error) {
	var result any
	decoded, err := decodeStrict(raw)
	value, isBool := decoded.(bool)

	if (err == nil) && (isBool == false) {
		err = ErrNotBool
	}

	if err == nil {
		result = value
	}

	return result, err
}

// decodeNumber decodes a JSON number.
func decodeNumber(raw json.RawMessage) (json.Number, error) {
	decoded, err := decodeStrict(raw)
	result, isNumber := decoded.(json.Number)

	if (err == nil) && (isNumber == false) {
		err = ErrNotNumber
	}

	return result, err
}

// validateInt decodes a whole number within the range and the ceiling.
func (s Setting) validateInt(raw json.RawMessage) (any, error) {
	var result any
	number, err := decodeNumber(raw)
	var parsed int64

	if err == nil {
		text := number.String()
		parsed, err = strconv.ParseInt(text, 10, 64)
	}

	if (err != nil) && (errors.Is(err, ErrNotNumber) == false) && (errors.Is(err, ErrTrailingData) == false) {
		err = ErrNotWholeNumber
	}

	if err == nil {
		err = s.checkRange(float64(parsed))
	}

	if err == nil {
		result = int(parsed)
	}

	return result, err
}

// validateDecimal decodes a number with at most Scale decimals within the
// range and the ceiling.
func (s Setting) validateDecimal(raw json.RawMessage) (any, error) {
	var result any
	number, err := decodeNumber(raw)
	var parsed float64

	if err == nil {
		parsed, err = number.Float64()
	}

	if err == nil {
		err = s.checkScale(parsed)
	}

	if err == nil {
		err = s.checkRange(parsed)
	}

	if err == nil {
		result = parsed
	}

	return result, err
}

// scaleTolerance absorbs float rounding when counting decimals.
const scaleTolerance = 1e-9

// checkScale refuses more decimals than the setting allows.
func (s Setting) checkScale(value float64) error {
	var result error
	factor := math.Pow10(s.Scale)
	scaled := value * factor
	rounded := math.Round(scaled)
	magnitude := math.Abs(scaled)
	tolerance := scaleTolerance * math.Max(1, magnitude)
	isWithinScale := (math.Abs(scaled-rounded) <= tolerance)
	isFinite := (math.IsInf(value, 0) == false) && (math.IsNaN(value) == false)

	if (isWithinScale == false) || (isFinite == false) {
		result = fmt.Errorf("must have at most %d decimals", s.Scale)
	}

	return result
}

// checkRange applies Min, Max and the hard ceiling.
func (s Setting) checkRange(value float64) error {
	var result error
	isBelowMin := (s.Min != nil) && (value < *s.Min)
	isAtExclusiveMin := (s.Min != nil) && (s.MinExclusive == true) && (value == *s.Min)
	isAboveMax := (s.Max != nil) && (value > *s.Max)
	isAboveCeiling := (s.Ceiling != nil) && (value > *s.Ceiling)

	switch {
	case isBelowMin == true:
		result = fmt.Errorf("must be at least %s", formatBound(*s.Min))
	case isAtExclusiveMin == true:
		result = fmt.Errorf("must be above %s", formatBound(*s.Min))
	case isAboveMax == true:
		result = fmt.Errorf("must be at most %s", formatBound(*s.Max))
	case isAboveCeiling == true:
		result = fmt.Errorf("must be at most %s, the hard ceiling (raising it needs a code change)", formatBound(*s.Ceiling))
	}

	return result
}

// formatBound prints a bound without trailing zeros.
func formatBound(value float64) string {
	result := strconv.FormatFloat(value, 'f', -1, 64)

	return result
}

// decodeString decodes a JSON string.
func decodeString(raw json.RawMessage) (string, error) {
	decoded, err := decodeStrict(raw)
	result, isString := decoded.(string)

	if (err == nil) && (isString == false) {
		err = ErrNotString
	}

	return result, err
}

// validateEnum decodes one of the allowed names.
func (s Setting) validateEnum(raw json.RawMessage) (any, error) {
	var result any
	value, err := decodeString(raw)

	if (err == nil) && (slices.Contains(s.Enum, value) == false) {
		err = s.enumError()
	}

	if err == nil {
		result = value
	}

	return result, err
}

// enumError names the allowed values.
func (s Setting) enumError() error {
	names := strings.Join(s.Enum, ", ")
	result := fmt.Errorf("must be one of: %s", names)

	return result
}

// validateOrderedList decodes a non-empty list of allowed names, each once.
func (s Setting) validateOrderedList(raw json.RawMessage) (any, error) {
	var result any
	var names []string
	reader := bytes.NewReader(raw)
	decoder := json.NewDecoder(reader)
	err := decoder.Decode(&names)

	if err != nil {
		err = ErrNotList
	}

	if (err == nil) && (decoder.More() == true) {
		err = ErrTrailingData
	}

	if (err == nil) && (len(names) == 0) {
		err = ErrEmptyList
	}

	if err == nil {
		err = s.checkListNames(names)
	}

	if err == nil {
		result = names
	}

	return result, err
}

// checkListNames refuses unknown and repeated names.
func (s Setting) checkListNames(names []string) error {
	var result error
	seen := map[string]bool{}

	for _, name := range names {
		isKnown := slices.Contains(s.Enum, name)

		if (result == nil) && (isKnown == false) {
			result = s.enumError()
		}

		if (result == nil) && (seen[name] == true) {
			result = ErrRepeatedListItem
		}

		seen[name] = true
	}

	return result
}

// validateHostValue decodes a hostname and checks it (see ValidateHost).
func validateHostValue(raw json.RawMessage) (any, error) {
	var result any
	value, err := decodeString(raw)

	if err == nil {
		value, err = ValidateHost(value)
	}

	if err == nil {
		result = value
	}

	return result, err
}

// ValidateHost checks a hostname for a free extraction service and returns
// it lowercased: a hostname only (no scheme, port, path, user or IP literal),
// at least two labels of letters, digits and hyphens, at most 253 characters,
// no trailing dot.
func ValidateHost(host string) (string, error) {
	var err error
	result := strings.ToLower(host)
	ip := net.ParseIP(result)
	labels := strings.Split(result, hostSeparator)
	lastLabel := labels[len(labels)-1]

	switch {
	case result == "":
		err = ErrHostEmpty
	case len(result) > hostMaxLength:
		err = ErrHostTooLong
	case (ip != nil) || (isAllDigits(lastLabel) == true):
		err = ErrHostIP
	case hasOnlyHostCharacters(result) == false:
		err = ErrHostCharacter
	case len(labels) < hostMinLabels:
		err = ErrHostLabels
	case hasValidLabels(labels) == false:
		err = ErrHostLabel
	}

	if err != nil {
		result = ""
	}

	return result, err
}

// hasOnlyHostCharacters reports lowercase ASCII letters, digits, hyphens and
// dots only.
func hasOnlyHostCharacters(host string) bool {
	result := true

	for _, char := range host {
		isLetter := (char >= 'a') && (char <= 'z')
		isDigit := (char >= '0') && (char <= '9')
		isAllowed := (isLetter == true) || (isDigit == true) || (char == '-') || (char == '.')
		result = result && isAllowed
	}

	return result
}

// hasValidLabels reports labels of 1-63 characters that neither start nor
// end with a hyphen.
func hasValidLabels(labels []string) bool {
	result := true

	for _, label := range labels {
		isSized := (len(label) > 0) && (len(label) <= hostLabelMaxLength)
		isHyphenated := (strings.HasPrefix(label, hostLabelHyphen) == true) || (strings.HasSuffix(label, hostLabelHyphen) == true)
		result = result && (isSized == true) && (isHyphenated == false)
	}

	return result
}

// isAllDigits reports a non-empty label of digits only (an IPv4-like tail).
func isAllDigits(label string) bool {
	result := (label != "")

	for _, char := range label {
		result = result && (char >= '0') && (char <= '9')
	}

	return result
}

// NeedsApproval reports whether changing the setting from current to next
// waits for the owner's Approve in Telegram. Both are typed values as
// Validate returns them, or the .env / default values the bot uses.
func (s Setting) NeedsApproval(current any, next any) bool {
	result := false
	currentNumber, isCurrentNumber := asFloat(current)
	nextNumber, isNextNumber := asFloat(next)
	areNumbers := (isCurrentNumber == true) && (isNextNumber == true)

	switch s.Approval {
	case ApprovalAlways:
		result = (SameValue(current, next) == false)
	case ApprovalRaise:
		result = (areNumbers == false) || (nextNumber > currentNumber)
	case ApprovalLower:
		result = (areNumbers == false) || (nextNumber < currentNumber)
	case ApprovalToTrue:
		result = (next == true) && (current != true)
	}

	return result
}

// asFloat reads an int or float64 value.
func asFloat(value any) (float64, bool) {
	result := 0.0
	isNumber := true

	switch typed := value.(type) {
	case int:
		result = float64(typed)
	case int64:
		result = float64(typed)
	case float64:
		result = typed
	default:
		isNumber = false
	}

	return result, isNumber
}

// SameValue reports whether two values are equal as JSON: 20 and 20.0, and
// a typed value and its JSON encoding, are the same.
func SameValue(left any, right any) bool {
	leftNormal, leftErr := normalize(left)
	rightNormal, rightErr := normalize(right)
	result := (leftErr == nil) && (rightErr == nil) && (reflect.DeepEqual(leftNormal, rightNormal) == true)

	return result
}

// normalize turns a value (or raw JSON) into the generic JSON form with
// float64 numbers.
func normalize(value any) (any, error) {
	var result any
	raw, isRaw := value.(json.RawMessage)
	var err error

	if isRaw == false {
		raw, err = json.Marshal(value)
	}

	if err == nil {
		err = json.Unmarshal(raw, &result)
	}

	return result, err
}
