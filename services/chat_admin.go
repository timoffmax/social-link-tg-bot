package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api/request"
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
		Status            string `json:"status"`
		IsMember          bool   `json:"is_member"`
		CanDeleteMessages bool   `json:"can_delete_messages"`
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

// botTokenIDSeparator ends the bot's numeric id at the start of its token.
const botTokenIDSeparator = ":"

// errBotIDUnknown is returned when the token has no numeric bot id; it
// never carries the token.
var errBotIDUnknown = errors.New("the bot token has no numeric bot id")

// FetchBotDeleteRight asks Telegram whether the bot may delete members'
// messages in the chat: it is the creator, or an administrator with the
// "Delete messages" right. A failed check returns false with the error,
// meaning the right is unknown, so callers claim neither way. Nothing is
// remembered here; the app keeps a pruned cache of the answers.
func FetchBotDeleteRight(chatID int64) (bool, error) {
	answer := chatMemberAnswer{}
	botID, err := botUserID()

	if err == nil {
		answer, err = getChatMemberAnswer(chatID, botID)
	}

	status := answer.Result.Status
	isCreator := (status == chatMemberStatusCreator)
	isAdminWithRight := (status == chatMemberStatusAdministrator) && (answer.Result.CanDeleteMessages == true)
	result := (err == nil) && ((isCreator == true) || (isAdminWithRight == true))

	return result, err
}

// botUserID returns the bot's own user id: the number before ':' in its
// token.
func botUserID() (int64, error) {
	token := config.GetTgBotToken()
	idText, _, _ := strings.Cut(token, botTokenIDSeparator)
	result, err := strconv.ParseInt(idText, 10, 64)

	if err != nil {
		err = errBotIDUnknown
	}

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
		err = request.WithoutRequestURL(chatMemberMethod, err)
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
		err = request.WithoutRequestURL(chatInfoMethod, err)
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
