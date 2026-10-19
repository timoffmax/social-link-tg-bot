package app

import (
	"strings"
	"testing"
)

func useGroupPrivacy(t *testing.T, state int32) {
	previous := groupPrivacyState.Load()
	groupPrivacyState.Store(state)
	t.Cleanup(func() { groupPrivacyState.Store(previous) })
}

func useGroupPrivacyAlerts(t *testing.T) *[]string {
	alerts := []string{}
	previous := notifyGroupPrivacy
	notifyGroupPrivacy = func(alertKey string, text string) bool {
		alerts = append(alerts, alertKey)

		return true
	}
	t.Cleanup(func() { notifyGroupPrivacy = previous })

	return &alerts
}

func TestGroupPrivacyOnIsLoggedAndSentToTheAdmin(t *testing.T) {
	useGroupPrivacy(t, groupPrivacyUnknown)
	alerts := useGroupPrivacyAlerts(t)
	logged := captureLog(t)

	recordGroupPrivacy(false)

	if (len(*alerts) != 1) || ((*alerts)[0] != groupPrivacyAlertKey) || (strings.Contains(logged.String(), "Group Privacy is on") == false) {
		t.Fatalf("expected one alert and a warning, got %v / %q", *alerts, logged.String())
	}

	if isGroupPrivacyOff() == true {
		t.Fatalf("Group Privacy on must not count as off")
	}
}

func TestGroupPrivacyOffIsRecordedWithoutAWarning(t *testing.T) {
	useGroupPrivacy(t, groupPrivacyUnknown)
	alerts := useGroupPrivacyAlerts(t)
	logged := captureLog(t)

	recordGroupPrivacy(true)

	if (len(*alerts) != 0) || (logged.Len() != 0) || (isGroupPrivacyOff() == false) {
		t.Fatalf("expected no alert or warning, got %v / %q", *alerts, logged.String())
	}
}

func TestUnknownGroupPrivacyDoesNotCountAsOff(t *testing.T) {
	useGroupPrivacy(t, groupPrivacyUnknown)

	if isGroupPrivacyOff() == true {
		t.Fatalf("an unread state must not count as off")
	}
}
