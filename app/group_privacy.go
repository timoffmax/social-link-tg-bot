package app

import (
	"fmt"
	"log"
	"sync/atomic"

	"github.com/timoffmax/social-link-bot/services"
)

// Group privacy states, from getMe's can_read_all_group_messages.
const (
	groupPrivacyUnknown int32 = iota
	groupPrivacyOff
	groupPrivacyOn
)

// groupPrivacyAlertKey names the admin alert about Group Privacy being on.
const groupPrivacyAlertKey = "group-privacy-mode"

// groupPrivacyWarning explains what Group Privacy on means for the bot.
const groupPrivacyWarning = "Group Privacy is on (getMe: can_read_all_group_messages=false): in groups where the bot isn't an admin it only sees commands, replies to its messages and mentions, so links posted there are missed. Turn it off in BotFather: /mybots → Bot Settings → Group Privacy → Turn off; groups the bot is already in may need it removed and added again."

// groupPrivacyState holds the state read at startup; unknown until getMe
// answers.
var groupPrivacyState atomic.Int32

// notifyGroupPrivacy alerts the admin; a variable for tests.
var notifyGroupPrivacy = services.NotifyAdmin

// recordGroupPrivacy stores getMe's can_read_all_group_messages; when the bot
// can't read all group messages, it logs a warning and tells the admin.
func recordGroupPrivacy(canReadAllGroupMessages bool) {
	state := groupPrivacyOn

	if canReadAllGroupMessages == true {
		state = groupPrivacyOff
	}

	groupPrivacyState.Store(state)

	if canReadAllGroupMessages == false {
		log.Printf("⚠️ %s", groupPrivacyWarning)
		alertText := fmt.Sprintf("⚠️ %s", groupPrivacyWarning)
		notifyGroupPrivacy(groupPrivacyAlertKey, alertText)
	}
}

// isGroupPrivacyOff reports whether getMe confirmed that the bot reads every
// group message, so it needs no admin rights to see links.
func isGroupPrivacyOff() bool {
	state := groupPrivacyState.Load()
	result := (state == groupPrivacyOff)

	return result
}
