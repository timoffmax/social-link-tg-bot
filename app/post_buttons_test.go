package app

import (
	"testing"

	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// stubGroupButtons sets the groups' button settings without a database.
func stubGroupButtons(t *testing.T, isReportOn, isSupportOn bool) {
	previous := getGroupButtonSettings
	previousCache := groupButtonSettings
	groupButtonSettings = newTTLStore(groupButtonSettingsTTL)
	getGroupButtonSettings = func(int64) (services.ChatButtonSettings, error) {
		return services.ChatButtonSettings{IsReportEnabled: isReportOn, IsSupportEnabled: isSupportOn}, nil
	}
	t.Cleanup(func() {
		getGroupButtonSettings = previous
		groupButtonSettings = previousCache
	})
}

func postButtonCallbacks(markup api.TgReplyMarkupInterface) []string {
	var result []string
	keyboard, isKeyboard := markup.(*api.TgInlineKeyboardMarkup)

	if isKeyboard == true {
		for _, button := range keyboard.InlineKeyboard[0] {
			result = append(result, button.CallbackData)
		}
	}

	return result
}

func TestPrivatePostsGetSupportAndReportButAlbumItemsNone(t *testing.T) {
	private := &response.TgChat{Id: 42, Type: chatTypePrivate}
	callbacks := postButtonCallbacks(postButtonsMarkup(private, true))

	if (len(callbacks) != 2) || (callbacks[0] != donateCommand) || (callbacks[1] != reportPostCallback) {
		t.Fatalf("expected support then report, got %v", callbacks)
	}

	if postButtonsMarkup(private, false) != nil {
		t.Fatalf("album items must not get buttons")
	}
}

func TestGroupPostsGetReportAlwaysAndSupportEveryTenthPost(t *testing.T) {
	stubGroupButtons(t, true, true)
	previousUsername := botUsername
	botUsername = "fix_link_test_bot"
	t.Cleanup(func() { botUsername = previousUsername })
	group := &response.TgChat{Id: -900, Type: "supergroup"}
	supportCount := 0

	for i := 0; i < 2*groupSupportEvery; i++ {
		callbacks := postButtonCallbacks(postButtonsMarkup(group, true))

		if callbacks[len(callbacks)-1] != reportPostCallback {
			t.Fatalf("post %d: expected the report button, got %v", i, callbacks)
		}

		if len(callbacks) == 2 {
			supportCount++
		}
	}

	if supportCount != 2 {
		t.Fatalf("expected the support button on 2 of %d posts, got %d", 2*groupSupportEvery, supportCount)
	}
}

func TestGroupButtonsFollowTheGroupSettings(t *testing.T) {
	stubGroupButtons(t, false, false)
	group := &response.TgChat{Id: -901, Type: "supergroup"}

	for i := 0; i < groupSupportEvery; i++ {
		if markup := postButtonsMarkup(group, true); markup != nil {
			t.Fatalf("post %d: expected no buttons with both turned off, got %v", i, postButtonCallbacks(markup))
		}
	}
}

func TestPostButtonToggleFlipsTheReportSetting(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	stubGroupButtons(t, true, true)
	stored := map[int64]bool{}
	previousSet := setGroupReportEnabled
	setGroupReportEnabled = func(chatID int64, enabled bool) error {
		stored[chatID] = enabled
		return nil
	}
	t.Cleanup(func() { setGroupReportEnabled = previousSet })

	handlePostButtonToggleCallback(&response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 7}, Message: response.TgMessage{Chat: response.TgChat{Id: -902, Type: "supergroup"}}, Data: reportButtonToggleCallback})

	calls := recorder.all()
	toast := calls[len(calls)-1]

	if (stored[-902] != false) || (toast.method != "answerCallbackQuery") || (toast.body["text"] != reportButtonNowOffText) {
		t.Fatalf("expected the report button turned off and confirmed, got %v %+v", stored, calls)
	}
}

func TestGroupSupportButtonLinksToThePrivateChat(t *testing.T) {
	previousUsername := botUsername
	botUsername = "fix_link_test_bot"
	t.Cleanup(func() { botUsername = previousUsername })

	groupButton := postSupportButton(&response.TgChat{Id: -100, Type: "supergroup"})
	privateButton := postSupportButton(&response.TgChat{Id: 42, Type: chatTypePrivate})

	if (groupButton.URL != "https://t.me/fix_link_test_bot?start=donate") || (groupButton.CallbackData != "") {
		t.Fatalf("expected a link to the private chat in groups, got %+v", groupButton)
	}

	if (privateButton.CallbackData != donateCommand) || (privateButton.URL != "") {
		t.Fatalf("expected /donate in place in private chats, got %+v", privateButton)
	}
}

func TestStaleReadDoesNotCacheOverAToggle(t *testing.T) {
	stubGroupButtons(t, true, true)
	key := groupButtonsKey(-950)
	versionBefore := groupButtonsVersion(key)

	invalidateGroupButtons(key)
	cacheGroupButtons(key, services.ChatButtonSettings{IsReportEnabled: true}, versionBefore)

	if _, isCached := groupButtonSettings.get(key); isCached == true {
		t.Fatalf("a read from before the toggle must not be cached")
	}
}
