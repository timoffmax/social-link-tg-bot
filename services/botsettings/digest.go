package botsettings

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// Change digest: every applied change reaches the owner in Telegram, at most
// one message per digestInterval. It can not be muted, so a change made
// through the dashboard (or psql) by someone else never goes unnoticed.
const (
	digestInterval   = 5 * time.Minute
	digestMaxLines   = 20
	digestHeading    = "⚙️ Settings changed:"
	digestLine       = "• %s (%s, %s)"
	digestMoreLines  = "…and %d more change(s): see the dashboard's audit log."
	digestTimeLayout = "15:04"
	digestOn         = "on"
	digestOff        = "off"
	digestNone       = "none"
	digestResetNote  = " (reset to .env / default)"
	digestValueArrow = "%s %s → %s"
	digestValueSet   = "%s %s"
	// digestUnknownKey names a bot_settings key the registry does not know
	// (manual psql): the bot ignores it, so it has no values to show.
	digestUnknownKey = "%s (not a known setting, ignored)"
	// digestActorOutside names a change the reload found in bot_settings,
	// made outside the bot (manual psql).
	digestActorOutside = "outside the bot"
)

// Digest line of an applied special-download limit: ids and numbers only.
const (
	digestSpecialLimit     = "Special downloads a month, %s: %s → %s"
	digestSpecialUser      = "user %d"
	digestSpecialChat      = "group %d"
	digestSpecialDefault   = "default (%d)"
	digestSpecialUnlimited = "unlimited"
	digestSpecialUnlimit   = int64(-1)
)

// Owner timezone of the digest times; UTC when the system has no zone data.
const (
	digestTimezone       = "Europe/Kyiv"
	digestTimezoneLegacy = "Europe/Kiev"
)

// digestEntry is one applied change, rendered by the bot from the registry.
type digestEntry struct {
	At    time.Time
	Text  string
	Actor string
}

// changeDigest collects applied changes until the next message may be sent.
type changeDigest struct {
	entries    []digestEntry
	lastSentAt time.Time
	location   *time.Location
}

// newChangeDigest returns an empty digest in the owner's timezone.
func newChangeDigest() *changeDigest {
	location := loadDigestLocation()
	result := &changeDigest{location: location}

	return result
}

// loadDigestLocation loads the owner's timezone, with the old zone name and
// UTC as fallbacks.
func loadDigestLocation() *time.Location {
	result, err := time.LoadLocation(digestTimezone)

	if err != nil {
		result, err = time.LoadLocation(digestTimezoneLegacy)
	}

	if err != nil {
		result = time.UTC
	}

	return result
}

// add records one change.
func (d *changeDigest) add(entry digestEntry) {
	d.entries = append(d.entries, entry)
}

// isDue reports pending changes whose message may be sent now.
func (d *changeDigest) isDue(now time.Time) bool {
	isWindowOpen := (d.lastSentAt.IsZero() == true) || (now.Sub(d.lastSentAt) >= digestInterval)
	result := (len(d.entries) > 0) && (isWindowOpen == true)

	return result
}

// flush sends the pending changes as one message and empties the digest. A
// failed send keeps them for the next window, so a Telegram outage does not
// retry every 2 s.
func (d *changeDigest) flush(now time.Time, notify func(text string) bool) {
	text := d.render()
	isSent := false

	if notify != nil {
		isSent = notify(text)
	}

	if isSent == true {
		d.entries = nil
	}

	if isSent == false {
		log.Printf("⚠️ Could not send the settings change digest (%d change(s)); retrying in %s", len(d.entries), digestInterval)
	}

	d.lastSentAt = now
}

// render builds the message: one line per change, oldest first.
func (d *changeDigest) render() string {
	lines := []string{digestHeading}
	shown := d.entries

	if len(shown) > digestMaxLines {
		shown = shown[:digestMaxLines]
	}

	for _, entry := range shown {
		at := entry.At.In(d.location)
		atText := at.Format(digestTimeLayout)
		line := fmt.Sprintf(digestLine, entry.Text, atText, entry.Actor)
		lines = append(lines, line)
	}

	if len(d.entries) > digestMaxLines {
		more := fmt.Sprintf(digestMoreLines, len(d.entries)-digestMaxLines)
		lines = append(lines, more)
	}

	result := strings.Join(lines, "\n")

	return result
}

// describeChange renders a change from the registry label and the old and
// new effective values: "TikTok off", "Default daily media limit 0 → 30". A
// key the registry does not know is named as ignored.
func describeChange(key string, oldValue any, newValue any, isReset bool) string {
	setting, isKnown := settings.Lookup(key)
	result := fmt.Sprintf(digestUnknownKey, key)

	if isKnown == true {
		result = describeKnownChange(setting, oldValue, newValue, isReset)
	}

	return result
}

// describeKnownChange renders a change of a registry setting.
func describeKnownChange(setting settings.Setting, oldValue any, newValue any, isReset bool) string {
	newText := formatDigestValue(newValue)
	oldText := formatDigestValue(oldValue)
	result := fmt.Sprintf(digestValueArrow, setting.Label, oldText, newText)

	if setting.Type == settings.TypeBool {
		result = fmt.Sprintf(digestValueSet, setting.Label, newText)
	}

	if isReset == true {
		result += digestResetNote
	}

	return result
}

// formatDigestValue prints a value for the owner: on/off, a list joined by
// commas, a number without trailing zeros.
func formatDigestValue(value any) string {
	result := fmt.Sprintf("%v", value)

	switch typed := value.(type) {
	case bool:
		result = digestOff

		if typed == true {
			result = digestOn
		}
	case []string:
		result = joinDigestList(typed)
	case []any:
		items := make([]string, 0, len(typed))

		for _, item := range typed {
			items = append(items, fmt.Sprintf("%v", item))
		}

		result = joinDigestList(items)
	case float64:
		result = fmt.Sprintf("%g", typed)
	}

	return result
}

// joinDigestList joins list items; an empty list is "none".
func joinDigestList(items []string) string {
	result := strings.Join(items, ", ")

	if len(items) == 0 {
		result = digestNone
	}

	return result
}

// effectiveValue is the value the bot uses for key right now.
func effectiveValue(key string) any {
	effective, _ := config.GetEffectiveSetting(key)
	result := effective.Value

	return result
}

// changedKeys lists the keys whose stored value differs between two sets,
// sorted.
func changedKeys(before []StoredValue, after []StoredValue) []string {
	beforeByKey := storedByKey(before)
	afterByKey := storedByKey(after)
	result := []string{}

	for key, value := range beforeByKey {
		other, isPresent := afterByKey[key]

		if (isPresent == false) || (settings.SameValue(value.Value, other.Value) == false) {
			result = append(result, key)
		}
	}

	for key := range afterByKey {
		_, isPresent := beforeByKey[key]

		if isPresent == false {
			result = append(result, key)
		}
	}

	sort.Strings(result)

	return result
}

// describeSpecialLimitChange renders a limit change: "Special downloads a
// month, group -1001: default (20) → 30".
func describeSpecialLimitChange(change specialLimitChange) string {
	subject := fmt.Sprintf(digestSpecialUser, change.Target.ID)

	if change.Target.Scope == settings.SpecialLimitScopeChat {
		subject = fmt.Sprintf(digestSpecialChat, change.Target.ID)
	}

	oldText := specialLimitText(change.Old, change.Target.Scope)
	newText := specialLimitText(change.New, change.Target.Scope)
	result := fmt.Sprintf(digestSpecialLimit, subject, oldText, newText)

	return result
}

// specialLimitText prints a stored limit: the default with its value,
// unlimited, or the number.
func specialLimitText(limit *int64, scope string) string {
	result := ""

	switch {
	case limit == nil:
		defaultLimit := specialLimitDefault(scope)
		result = fmt.Sprintf(digestSpecialDefault, defaultLimit)
	case *limit == digestSpecialUnlimit:
		result = digestSpecialUnlimited
	default:
		result = fmt.Sprintf("%d", *limit)
	}

	return result
}

// specialLimitDefault is the default monthly limit of a person or a group.
func specialLimitDefault(scope string) int {
	result := config.GetGatedFetchUserMonthlyAllowance()

	if scope == settings.SpecialLimitScopeChat {
		result = config.GetGatedFetchChatMonthlyCap()
	}

	return result
}
