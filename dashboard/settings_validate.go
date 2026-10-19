package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
)

// Value types of the published registry.
const (
	settingTypeBool        = "bool"
	settingTypeInt         = "int"
	settingTypeDecimal     = "decimal"
	settingTypeEnum        = "enum"
	settingTypeHost        = "host"
	settingTypeOrderedList = "ordered_list"
)

// Host limits (RFC 1035).
const (
	hostMaxLength      = 253
	hostLabelMaxLength = 63
	hostMinLabels      = 2
	hostSeparator      = "."
	hostLabelHyphen    = "-"
)

// Whole-number parsing of an int value.
const (
	intValueBase = 10
	intValueBits = 64
)

// scaleTolerance absorbs float rounding when counting decimals.
const scaleTolerance = 1e-9

// Validation messages of a setting value, worded for the owner like the
// bot's own.
const (
	errSettingNotBool       = "must be true or false"
	errSettingNotNumber     = "must be a number"
	errSettingNotWhole      = "must be a whole number"
	errSettingNotText       = "must be text"
	errSettingNotList       = "must be a list of names"
	errSettingEmptyList     = "must name at least one"
	errSettingRepeatedItem  = "must not name one twice"
	errSettingSingleValue   = "must be a single value"
	errSettingScale         = "must have at most %d decimals"
	errSettingAtLeast       = "must be at least %s"
	errSettingAbove         = "must be above %s"
	errSettingAtMost        = "must be at most %s"
	errSettingCeiling       = "must be at most %s, the hard ceiling (raising it needs a code change)"
	errSettingOneOf         = "must be one of: %s"
	errSettingUnknownType   = "has a type this dashboard does not know (%q)"
	errHostEmpty            = "must not be empty"
	errHostTooLong          = "must be at most 253 characters"
	errHostIP               = "must be a hostname, not an IP address"
	errHostLabels           = "must have at least two labels, like example.com"
	errHostCharacter        = "must be a hostname only (letters, digits, hyphens and dots; no scheme, port, path or user)"
	errHostLabel            = "has a label that is empty, longer than 63 characters, or starts or ends with a hyphen"
	settingValueErrorFormat = "%s %s"
	enumSeparator           = ", "
)

// Type errors of a setting value.
var (
	errSingleValue = errors.New(errSettingSingleValue)
	errNotBool     = errors.New(errSettingNotBool)
	errNotNumber   = errors.New(errSettingNotNumber)
	errNotText     = errors.New(errSettingNotText)
)

// validateSettingValue checks a requested value with the published registry
// entry, as the bot will: type, range, hard ceiling, decimals, names and
// host syntax. It is instant feedback only; the bot validates again and its
// decision counts.
func validateSettingValue(setting RegistrySetting, raw json.RawMessage) error {
	var err error

	switch setting.Type {
	case settingTypeBool:
		err = checkBoolValue(raw)
	case settingTypeInt:
		err = checkIntValue(setting, raw)
	case settingTypeDecimal:
		err = checkDecimalValue(setting, raw)
	case settingTypeEnum:
		err = checkEnumValue(setting, raw)
	case settingTypeHost:
		err = checkHostValue(raw)
	case settingTypeOrderedList:
		err = checkOrderedListValue(setting, raw)
	default:
		err = fmt.Errorf(errSettingUnknownType, setting.Type)
	}

	if err != nil {
		message := err.Error()
		err = badRequest(settingValueErrorFormat, setting.Key, message)
	}

	return err
}

// decodeSingleValue decodes exactly one JSON value, numbers as json.Number.
func decodeSingleValue(raw json.RawMessage) (interface{}, error) {
	var result interface{}
	reader := bytes.NewReader(raw)
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	err := decoder.Decode(&result)

	if (err == nil) && (decoder.More() == true) {
		err = errSingleValue
	}

	return result, err
}

// decodeAs decodes one JSON value and checks its Go type; mismatch is the
// error for a value of another type.
func decodeAs[T any](raw json.RawMessage, mismatch error) (T, error) {
	var result T
	decoded, err := decodeSingleValue(raw)
	value, isType := decoded.(T)

	if (err == nil) && (isType == false) {
		err = mismatch
	}

	if err == nil {
		result = value
	}

	return result, err
}

// checkBoolValue accepts a JSON boolean.
func checkBoolValue(raw json.RawMessage) error {
	_, err := decodeAs[bool](raw, errNotBool)

	return err
}

// checkIntValue accepts a whole number within the range and the ceiling.
func checkIntValue(setting RegistrySetting, raw json.RawMessage) error {
	number, err := decodeAs[json.Number](raw, errNotNumber)
	var parsed int64

	if err == nil {
		text := number.String()
		parsed, err = strconv.ParseInt(text, intValueBase, intValueBits)
	}

	if (err != nil) && (errors.Is(err, errNotNumber) == false) && (errors.Is(err, errSingleValue) == false) {
		err = errors.New(errSettingNotWhole)
	}

	if err == nil {
		err = checkRange(setting, float64(parsed))
	}

	return err
}

// checkDecimalValue accepts a number with at most Scale decimals within the
// range and the ceiling.
func checkDecimalValue(setting RegistrySetting, raw json.RawMessage) error {
	number, err := decodeAs[json.Number](raw, errNotNumber)
	var parsed float64

	if err == nil {
		parsed, err = number.Float64()
	}

	if err == nil {
		err = checkScale(setting.Scale, parsed)
	}

	if err == nil {
		err = checkRange(setting, parsed)
	}

	return err
}

// checkScale refuses more decimals than allowed.
func checkScale(scale int, value float64) error {
	var result error
	factor := math.Pow10(scale)
	scaled := value * factor
	rounded := math.Round(scaled)
	magnitude := math.Abs(scaled)
	tolerance := scaleTolerance * math.Max(1, magnitude)
	isWithinScale := (math.Abs(scaled-rounded) <= tolerance)
	isFinite := (math.IsInf(value, 0) == false) && (math.IsNaN(value) == false)

	if (isWithinScale == false) || (isFinite == false) {
		result = fmt.Errorf(errSettingScale, scale)
	}

	return result
}

// checkRange applies Min, Max and the hard ceiling.
func checkRange(setting RegistrySetting, value float64) error {
	var result error
	isBelowMin := (setting.Min != nil) && (value < *setting.Min)
	isAtExclusiveMin := (setting.Min != nil) && (setting.MinExclusive == true) && (value == *setting.Min)
	isAboveMax := (setting.Max != nil) && (value > *setting.Max)
	isAboveCeiling := (setting.Ceiling != nil) && (value > *setting.Ceiling)

	switch {
	case isBelowMin == true:
		bound := formatBound(*setting.Min)
		result = fmt.Errorf(errSettingAtLeast, bound)
	case isAtExclusiveMin == true:
		bound := formatBound(*setting.Min)
		result = fmt.Errorf(errSettingAbove, bound)
	case isAboveMax == true:
		bound := formatBound(*setting.Max)
		result = fmt.Errorf(errSettingAtMost, bound)
	case isAboveCeiling == true:
		bound := formatBound(*setting.Ceiling)
		result = fmt.Errorf(errSettingCeiling, bound)
	}

	return result
}

// formatBound prints a bound without trailing zeros.
func formatBound(value float64) string {
	result := strconv.FormatFloat(value, 'f', -1, 64)

	return result
}

// checkEnumValue accepts one of the allowed names.
func checkEnumValue(setting RegistrySetting, raw json.RawMessage) error {
	value, err := decodeAs[string](raw, errNotText)

	if (err == nil) && (slices.Contains(setting.Enum, value) == false) {
		err = enumError(setting)
	}

	return err
}

// enumError names the allowed values.
func enumError(setting RegistrySetting) error {
	names := strings.Join(setting.Enum, enumSeparator)
	result := fmt.Errorf(errSettingOneOf, names)

	return result
}

// checkOrderedListValue accepts a non-empty list of allowed names, each once.
func checkOrderedListValue(setting RegistrySetting, raw json.RawMessage) error {
	var names []string
	reader := bytes.NewReader(raw)
	decoder := json.NewDecoder(reader)
	err := decoder.Decode(&names)

	if err != nil {
		err = errors.New(errSettingNotList)
	}

	if (err == nil) && (decoder.More() == true) {
		err = errSingleValue
	}

	if (err == nil) && (len(names) == 0) {
		err = errors.New(errSettingEmptyList)
	}

	if err == nil {
		err = checkListNames(setting, names)
	}

	return err
}

// checkListNames refuses unknown and repeated names.
func checkListNames(setting RegistrySetting, names []string) error {
	var result error
	seen := map[string]bool{}

	for _, name := range names {
		isKnown := slices.Contains(setting.Enum, name)

		if (result == nil) && (isKnown == false) {
			result = enumError(setting)
		}

		if (result == nil) && (seen[name] == true) {
			result = errors.New(errSettingRepeatedItem)
		}

		seen[name] = true
	}

	return result
}

// checkHostValue accepts a hostname: no scheme, port, path, user or IP
// literal, at least two labels of letters, digits and hyphens, at most 253
// characters. Upper case is accepted; the bot stores it lowercased.
func checkHostValue(raw json.RawMessage) error {
	host, err := decodeAs[string](raw, errNotText)

	if err == nil {
		err = checkHost(host)
	}

	return err
}

// checkHost checks a hostname (see checkHostValue).
func checkHost(host string) error {
	var result error
	lower := strings.ToLower(host)
	ip := net.ParseIP(lower)
	labels := strings.Split(lower, hostSeparator)
	lastLabel := labels[len(labels)-1]

	switch {
	case lower == "":
		result = errors.New(errHostEmpty)
	case len(lower) > hostMaxLength:
		result = errors.New(errHostTooLong)
	case (ip != nil) || (isAllDigits(lastLabel) == true):
		result = errors.New(errHostIP)
	case hasOnlyHostCharacters(lower) == false:
		result = errors.New(errHostCharacter)
	case len(labels) < hostMinLabels:
		result = errors.New(errHostLabels)
	case hasValidLabels(labels) == false:
		result = errors.New(errHostLabel)
	}

	return result
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
