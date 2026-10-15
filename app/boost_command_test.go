package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// stubGroupBoost makes the group open or closed for boosts, with a title.
func stubGroupBoost(t *testing.T, isOpen bool, titleErr error) {
	previousCan := canBoostGroup
	previousTitle := getGroupTitle
	previousBonus := getGroupBoostBonus
	previousMember := isGroupMember
	previousReachable := isGroupReachable
	isGroupMember = func(int64, int64) (bool, error) { return true, nil }
	isGroupReachable = func(int64) bool { return true }
	canBoostGroup = func(int64) bool { return isOpen }
	getGroupTitle = func(int64) (string, error) { return "Memes <3", titleErr }
	getGroupBoostBonus = func(int64) (int, error) { return 7, nil }
	previousUsername := botUsername
	botUsername = "fix_link_test_bot"
	t.Cleanup(func() {
		canBoostGroup = previousCan
		getGroupTitle = previousTitle
		getGroupBoostBonus = previousBonus
		isGroupMember = previousMember
		isGroupReachable = previousReachable
		botUsername = previousUsername
	})
}

func TestBoostInAGroupLinksToThePrivateBoostScreen(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	stubGroupBoost(t, true, nil)

	handleBoostCommand(&response.TgMessage{MessageId: 5, Text: "/boost"}, &response.TgChat{Id: -1001, Type: "supergroup"}, &response.TgUser{Id: 42})

	calls := recorder.all()
	rows := donateReplyRows(calls[0])
	buttons, _ := rows[0].([]interface{})
	button, _ := buttons[0].(map[string]interface{})

	if button["url"] != "https://t.me/fix_link_test_bot?start=boost_-1001" {
		t.Fatalf("expected the private boost link, got %+v", button)
	}
}

func TestBoostIsUnavailableWhileStarsIsOff(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	stubGroupBoost(t, true, nil)

	handleBoostCommand(&response.TgMessage{MessageId: 5, Text: "/boost"}, &response.TgChat{Id: -1001, Type: "supergroup"}, &response.TgUser{Id: 42})

	if text, _ := recorder.all()[0].body["text"].(string); text != boostUnavailableText {
		t.Fatalf("expected the unavailable text, got %q", text)
	}
}

func TestBoostScreenShowsTheGroupAndTiers(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	stubGroupBoost(t, true, nil)

	handleStartOrDeepLink(&response.TgMessage{MessageId: 5, Text: "/start boost_-1001"}, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42})

	calls := recorder.all()
	text, _ := calls[0].body["text"].(string)
	rows := donateReplyRows(calls[0])

	if (strings.Contains(text, "Memes &lt;3") == false) || (strings.Contains(text, "Group bonus downloads now: 7") == false) || (len(rows) != 3) {
		t.Fatalf("expected the escaped title, balance and three tiers, got %q %+v", text, rows)
	}
}

func TestBoostScreenRefusesClosedOrUnknownGroups(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	stubGroupBoost(t, false, nil)

	handleStartOrDeepLink(&response.TgMessage{MessageId: 5, Text: "/start boost_-1001"}, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42})
	stubGroupBoost(t, true, errors.New("chat not found"))
	handleStartOrDeepLink(&response.TgMessage{MessageId: 6, Text: "/start boost_-1002"}, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42})

	calls := recorder.all()
	closedText, _ := calls[0].body["text"].(string)
	unknownText, _ := calls[1].body["text"].(string)

	if (closedText != boostClosedText) || (unknownText != boostUnknownGroupText) {
		t.Fatalf("unexpected texts %q / %q", closedText, unknownText)
	}
}

func TestBoostTierButtonSendsABoostInvoice(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	stubGroupBoost(t, true, nil)
	tier, chatID, isFound := parseBoostTierCallback("/boost_50_-1001")

	if (isFound == false) || (chatID != -1001) || (tier.Stars != 50) {
		t.Fatalf("unexpected parse %+v %d %t", tier, chatID, isFound)
	}

	handleBoostTierCallback(&response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: response.TgMessage{MessageId: 3, Chat: response.TgChat{Id: 42, Type: chatTypePrivate}}, Data: "/boost_50_-1001"})

	calls := recorder.all()
	invoicePayload, _ := calls[0].body["payload"].(string)

	if (calls[0].method != "sendInvoice") || (invoicePayload != "boost:50:42:-1001") {
		t.Fatalf("expected a boost invoice, got %+v", calls[0])
	}

	for _, data := range []string{"/boost_51_-1001", "/boost_50_1001", "/boost_50"} {
		if _, _, isFound := parseBoostTierCallback(data); isFound == true {
			t.Fatalf("expected %q to be rejected", data)
		}
	}
}

func TestBoostPreCheckoutChecksTheGroupNotThePayer(t *testing.T) {
	stubGroupBoost(t, false, nil)
	assumeSupportDBReady(t)

	if invoiceCreditsUsable("boost:50:42:-1001", 42) == true {
		t.Fatalf("a closed group must not be boosted")
	}

	if invoiceCreditsUsable("support:50:42", 42) == false {
		t.Fatalf("personal support follows the payer's check")
	}
}

func TestBoostPaymentThanksThePayerAndTellsTheGroup(t *testing.T) {
	stubGroupBoost(t, true, nil)
	record := services.SupportPaymentRecord{IsNew: true, Balance: 32}

	thanks, notice := boostPaymentTexts(record, 25, -1001, "Ann <b>")

	if (strings.Contains(thanks, "Memes &lt;3 got 25") == false) || (strings.Contains(notice, "Ann &lt;b&gt; boosted this group: +25") == false) {
		t.Fatalf("unexpected texts %q / %q", thanks, notice)
	}

	_, repeatNotice := boostPaymentTexts(services.SupportPaymentRecord{IsNew: false, Balance: 32}, 25, -1001, "Ann")

	if repeatNotice != "" {
		t.Fatalf("a repeated delivery must not notify the group again")
	}
}

func TestNonMembersGetNoBoostScreenOrInvoice(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	stubGroupBoost(t, true, nil)
	isGroupMember = func(int64, int64) (bool, error) { return false, nil }

	handleStartOrDeepLink(&response.TgMessage{MessageId: 5, Text: "/start boost_-1001"}, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42})
	handleBoostTierCallback(&response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: response.TgMessage{MessageId: 3, Chat: response.TgChat{Id: 42, Type: chatTypePrivate}}, Data: "/boost_50_-1001"})

	for _, call := range recorder.all() {
		text, _ := call.body["text"].(string)

		if (call.method == "sendInvoice") || (strings.Contains(text, "Memes") == true) {
			t.Fatalf("a non-member must not see the group or get an invoice, got %+v", call)
		}
	}
}

func TestUpgradedGroupMovesItsCredits(t *testing.T) {
	previous := moveGroupCredits
	moved := [2]int64{}
	moveGroupCredits = func(oldID int64, newID int64) error {
		moved = [2]int64{oldID, newID}
		return nil
	}
	t.Cleanup(func() { moveGroupCredits = previous })

	processUpdate(&response.TgUpdate{UpdateId: 990777, Message: response.TgMessage{MessageId: 9, Chat: response.TgChat{Id: -555, Type: "group"}, MigrateToChatId: -1009999}})

	if moved != [2]int64{-555, -1009999} {
		t.Fatalf("expected the credits moved to the supergroup, got %v", moved)
	}
}

func TestBoostPreCheckoutRefusesAGroupTheBotLeft(t *testing.T) {
	stubGroupBoost(t, true, nil)
	isGroupReachable = func(int64) bool { return false }

	if invoiceCreditsUsable("boost:50:42:-1001", 42) == true {
		t.Fatalf("an old invoice for a group the bot left must be refused at payment time")
	}
}
