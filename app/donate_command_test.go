package app

import (
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

// donateReplyRows returns the inline keyboard rows of a recorded reply.
func donateReplyRows(call botAPICall) []interface{} {
	markup, _ := call.body["reply_markup"].(map[string]interface{})
	rows, _ := markup["inline_keyboard"].([]interface{})

	return rows
}

// donateRowText returns the text of the first button of a keyboard row.
func donateRowText(row interface{}) string {
	buttons, _ := row.([]interface{})
	button, _ := buttons[0].(map[string]interface{})
	text, _ := button["text"].(string)

	return text
}

func TestDonateWhenStarsDisabledShowsCardAndCryptoOptions(t *testing.T) {
	recorder := startTestBotAPI(t, false)

	handleDonateCommand(&response.TgMessage{MessageId: 5}, &response.TgChat{Id: 42}, &response.TgUser{Id: 42})

	calls := recorder.all()
	rows := donateReplyRows(calls[0])
	text, _ := calls[0].body["text"].(string)

	if (len(calls) != 1) || (calls[0].method != "sendMessage") || (len(rows) != 3) {
		t.Fatalf("unexpected calls: %+v", calls)
	}

	if (strings.Index(text, donateCardText) < 0) || (strings.Index(text, donateCryptoText) < strings.Index(text, donateCardText)) || (strings.Contains(text, "Stars") == true) {
		t.Fatalf("expected the card, then the crypto options, got %q", text)
	}

	if (donateRowText(rows[0]) != donateCardButtonText) || (donateRowText(rows[1]) != donateCryptoButtonText) || (donateRowText(rows[2]) != donateBackButtonText) {
		t.Fatalf("unexpected buttons: %+v", rows)
	}
}

func TestDonateWhenStarsEnabledShowsTiersBeforeCardAndCryptoOptions(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	assumeSupportDBReady(t)

	handleDonateCommand(&response.TgMessage{MessageId: 5}, &response.TgChat{Id: 42}, &response.TgUser{Id: 42})

	calls := recorder.all()
	rows := donateReplyRows(calls[0])
	text, _ := calls[0].body["text"].(string)
	starsAt := strings.Index(text, donateStarsText)
	cardAt := strings.Index(text, donateCardText)
	cryptoAt := strings.Index(text, donateCryptoText)

	if (len(calls) != 1) || (len(rows) != 6) {
		t.Fatalf("unexpected calls: %+v", calls)
	}

	if (starsAt < 0) || (cardAt < starsAt) || (cryptoAt < cardAt) {
		t.Fatalf("expected the Stars section, then the card and crypto options, got %q", text)
	}

	if (strings.HasPrefix(donateRowText(rows[0]), "⭐") == false) || (donateRowText(rows[3]) != donateCardButtonText) || (donateRowText(rows[4]) != donateCryptoButtonText) || (donateRowText(rows[5]) != donateBackButtonText) {
		t.Fatalf("unexpected buttons: %+v", rows)
	}
}

func TestSupportCommandIsAnAliasOfDonate(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	assumeSupportDBReady(t)

	handleBotCommand(&response.TgMessage{MessageId: 5, Text: "/support"}, &response.TgChat{Id: 42, Type: "private"}, &response.TgUser{Id: 42})

	calls := recorder.all()
	text, _ := calls[0].body["text"].(string)

	if (len(calls) != 1) || (strings.Contains(text, donateStarsText) == false) || (strings.Contains(text, donateCryptoText) == false) {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestMainMenuOpensDonateWithTheSupportButton(t *testing.T) {
	keyboard := createMainMenuKeyboard(false, 42)
	isFound := false

	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			isFound = isFound || ((button.Text == supportMenuButtonText) && (button.CallbackData == donateCommand))
		}
	}

	if isFound == false {
		t.Fatalf("expected the %q button, got %+v", supportMenuButtonText, keyboard.InlineKeyboard)
	}
}

func TestBroadcastAnnouncementMentionsStarsOnlyWhenEnabled(t *testing.T) {
	enabled := buildBroadcastAnnouncement(true)
	disabled := buildBroadcastAnnouncement(false)

	if (strings.Contains(enabled, "Telegram Stars") == false) || (strings.Contains(enabled, "/donate") == false) {
		t.Fatalf("expected Stars and /donate, got %q", enabled)
	}

	if (strings.Contains(disabled, "Stars") == true) || (strings.Contains(disabled, "/donate") == false) {
		t.Fatalf("expected /donate without Stars, got %q", disabled)
	}
}

func TestDonateHidesStarsFromUsersWhoCantUseBonusDownloads(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	assumeSupportDBReady(t)
	supportCreditsUsable = func(int64) bool {
		return false
	}

	handleDonateCommand(&response.TgMessage{MessageId: 5}, &response.TgChat{Id: 42}, &response.TgUser{Id: 42})

	calls := recorder.all()
	rows := donateReplyRows(calls[0])
	text, _ := calls[0].body["text"].(string)

	if (len(rows) != 3) || (strings.Contains(text, "Stars") == true) {
		t.Fatalf("expected only the card and crypto options, got %q %+v", text, rows)
	}
}

func TestPostSupportButtonOnlyUnderWholePostsInPrivateChats(t *testing.T) {
	private := &response.TgChat{Id: 42, Type: chatTypePrivate}
	markup, isKeyboard := postSupportMarkup(private, true).(*api.TgInlineKeyboardMarkup)

	if (isKeyboard == false) || (len(markup.InlineKeyboard) != 1) || (len(markup.InlineKeyboard[0]) != 1) {
		t.Fatalf("expected one button in a private chat, got %+v", markup)
	}

	if markup.InlineKeyboard[0][0].CallbackData != donateCommand {
		t.Fatalf("expected the button to open /donate, got %+v", markup)
	}

	if postSupportMarkup(private, false) != nil {
		t.Fatalf("album items must not get the support button")
	}

	if postSupportMarkup(&response.TgChat{Id: -100, Type: "supergroup"}, true) != nil {
		t.Fatalf("groups must not get the support button")
	}
}
