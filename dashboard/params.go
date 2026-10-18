package dashboard

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Query parameter names.
const (
	paramDays   = "days"
	paramLimit  = "limit"
	paramMonths = "months"
	paramWeeks  = "weeks"
)

// Bounds every value is clamped to.
const (
	minDays   = 1
	maxDays   = 365
	minLimit  = 1
	maxLimit  = 200
	minMonths = 1
	maxMonths = 36
	minWeeks  = 1
	maxWeeks  = 104
)

// Defaults per endpoint.
const (
	defaultOverviewDays  = 90
	defaultPlatformsDays = 90
	defaultChatsDays     = 30
	defaultChatsLimit    = 20
	defaultMoneyMonths   = 12
	defaultReportsLimit  = 50
	defaultReportsWeeks  = 26
)

// intParam is an integer query parameter with a default and bounds.
type intParam struct {
	name     string
	fallback int
	min      int
	max      int
}

var (
	overviewDaysParam  = intParam{name: paramDays, fallback: defaultOverviewDays, min: minDays, max: maxDays}
	platformsDaysParam = intParam{name: paramDays, fallback: defaultPlatformsDays, min: minDays, max: maxDays}
	chatsDaysParam     = intParam{name: paramDays, fallback: defaultChatsDays, min: minDays, max: maxDays}
	chatsLimitParam    = intParam{name: paramLimit, fallback: defaultChatsLimit, min: minLimit, max: maxLimit}
	moneyMonthsParam   = intParam{name: paramMonths, fallback: defaultMoneyMonths, min: minMonths, max: maxMonths}
	reportsLimitParam  = intParam{name: paramLimit, fallback: defaultReportsLimit, min: minLimit, max: maxLimit}
	reportsWeeksParam  = intParam{name: paramWeeks, fallback: defaultReportsWeeks, min: minWeeks, max: maxWeeks}
)

// parse returns the parameter clamped to its bounds, the default when it is
// missing or empty, and an error when it is not an integer.
func (p intParam) parse(values url.Values) (int, error) {
	result := p.fallback
	var err error
	value := values.Get(p.name)
	raw := strings.TrimSpace(value)

	if raw != "" {
		result, err = p.parseValue(raw)
	}

	return result, err
}

// parseValue converts a non-empty raw value.
func (p intParam) parseValue(raw string) (int, error) {
	result := p.fallback
	value, err := strconv.Atoi(raw)

	if err == nil {
		result = p.clamp(value)
	}

	if err != nil {
		err = fmt.Errorf("invalid %s: must be an integer", p.name)
	}

	return result, err
}

// clamp keeps the value within the parameter's bounds.
func (p intParam) clamp(value int) int {
	result := value

	if result < p.min {
		result = p.min
	}

	if result > p.max {
		result = p.max
	}

	return result
}

// firstError returns the first non-nil error.
func firstError(errs ...error) error {
	var result error

	for _, err := range errs {
		if (result == nil) && (err != nil) {
			result = err
		}
	}

	return result
}
