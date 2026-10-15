package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// chatMemberMethod is the Bot API method that returns a member's status.
const chatMemberMethod = "getChatMember"

// Chat member statuses that may change a group's settings.
const (
	chatMemberStatusCreator       = "creator"
	chatMemberStatusAdministrator = "administrator"
)

// chatMemberAnswer is the part of a getChatMember answer the check needs.
type chatMemberAnswer struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		Status   string `json:"status"`
		IsMember bool   `json:"is_member"`
	} `json:"result"`
}

// Member statuses that count as being in the group (a restricted member is
// in it when is_member is set).
const (
	chatMemberStatusMember     = "member"
	chatMemberStatusRestricted = "restricted"
)

// IsChatMember asks Telegram whether the user is in the group; a failed
// check returns false with the error.
func IsChatMember(chatID int64, userID int64) (bool, error) {
	answer, err := getChatMemberAnswer(chatID, userID)
	status := answer.Result.Status
	isListed := (status == chatMemberStatusCreator) || (status == chatMemberStatusAdministrator) || (status == chatMemberStatusMember)
	isRestrictedMember := (status == chatMemberStatusRestricted) && (answer.Result.IsMember == true)
	result := (err == nil) && ((isListed == true) || (isRestrictedMember == true))

	return result, err
}

// IsChatAdmin asks Telegram whether the user is the group's creator or an
// administrator. A failed check returns false with the error, so callers
// refuse the change.
func IsChatAdmin(chatID int64, userID int64) (bool, error) {
	answer, err := getChatMemberAnswer(chatID, userID)
	status := answer.Result.Status
	result := (err == nil) && ((status == chatMemberStatusCreator) || (status == chatMemberStatusAdministrator))

	return result, err
}

// getChatMemberAnswer asks Telegram for the user's membership in the chat.
func getChatMemberAnswer(chatID int64, userID int64) (chatMemberAnswer, error) {
	var resp *http.Response
	answer := chatMemberAnswer{}
	token := config.GetTgBotToken()
	url := fmt.Sprintf(botAPIURLTemplate, token, chatMemberMethod)
	requestBody := map[string]interface{}{"chat_id": chatID, "user_id": userID}
	jsonBody, err := json.Marshal(requestBody)

	if err == nil {
		reader := bytes.NewReader(jsonBody)
		resp, err = botAPIClient.Post(url, "application/json", reader)
		err = withoutRequestURL(chatMemberMethod, err)
	}

	if err == nil {
		defer resp.Body.Close()
		err = json.NewDecoder(resp.Body).Decode(&answer)
	}

	if (err == nil) && (answer.Ok == false) {
		err = fmt.Errorf("%s failed: %s", chatMemberMethod, answer.Description)
	}

	return answer, err
}

// chatInfoMethod is the Bot API method that returns a chat's details.
const chatInfoMethod = "getChat"

// chatInfoAnswer is the part of a getChat answer a boost needs.
type chatInfoAnswer struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		Title string `json:"title"`
		Type  string `json:"type"`
	} `json:"result"`
}

// groupCheckClient bounds the group check made during a pre-checkout,
// which Telegram expects answered within 10 seconds.
var groupCheckClient = &http.Client{Timeout: 3 * time.Second}

// IsGroupReachable reports whether the bot is still in the group and its id
// is current (an upgraded group answers with an error); quick enough for a
// pre-checkout.
func IsGroupReachable(chatID int64) bool {
	_, err := getGroupTitle(groupCheckClient, chatID)
	result := (err == nil)

	return result
}

// GetGroupTitle asks Telegram for a group's title. It fails when the bot
// isn't in the chat or the chat isn't a group.
func GetGroupTitle(chatID int64) (string, error) {
	result, err := getGroupTitle(botAPIClient, chatID)

	return result, err
}

// getGroupTitle asks Telegram for a group's title with the given client.
func getGroupTitle(client *http.Client, chatID int64) (string, error) {
	var resp *http.Response
	answer := chatInfoAnswer{}
	token := config.GetTgBotToken()
	url := fmt.Sprintf(botAPIURLTemplate, token, chatInfoMethod)
	requestBody := map[string]interface{}{"chat_id": chatID}
	jsonBody, err := json.Marshal(requestBody)

	if err == nil {
		reader := bytes.NewReader(jsonBody)
		resp, err = client.Post(url, "application/json", reader)
		err = withoutRequestURL(chatInfoMethod, err)
	}

	if err == nil {
		defer resp.Body.Close()
		err = json.NewDecoder(resp.Body).Decode(&answer)
	}

	if (err == nil) && (answer.Ok == false) {
		err = fmt.Errorf("%s failed: %s", chatInfoMethod, answer.Description)
	}

	isGroup := (answer.Result.Type == chatTypeGroup) || (answer.Result.Type == chatTypeSupergroup)

	if (err == nil) && (isGroup == false) {
		err = fmt.Errorf("chat %d is not a group", chatID)
	}

	return answer.Result.Title, err
}

// Telegram chat types a group boost accepts.
const (
	chatTypeGroup      = "group"
	chatTypeSupergroup = "supergroup"
)
