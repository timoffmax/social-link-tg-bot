package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

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
		Status string `json:"status"`
	} `json:"result"`
}

// IsChatAdmin asks Telegram whether the user is the group's creator or an
// administrator. A failed check returns false with the error, so callers
// refuse the change.
func IsChatAdmin(chatID int64, userID int64) (bool, error) {
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

	status := answer.Result.Status
	result := (err == nil) && ((status == chatMemberStatusCreator) || (status == chatMemberStatusAdministrator))

	return result, err
}
