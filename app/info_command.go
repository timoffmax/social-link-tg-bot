package app

import (
	"fmt"
	"log"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

// infoCommand explains how the bot works, without the /start menu.
const infoCommand = "/info"

// infoText returns the /info reply: the intro, the platforms and the guide
// to private chats, groups, free and restricted content.
func infoText() string {
	isStarsEnabled := config.IsStarsSupportEnabled()
	guideText := startGuideText(isStarsEnabled)
	platformsText := startPlatformsText()
	result := fmt.Sprintf("%s%s%s", startIntroText, platformsText, guideText)

	return result
}

// handleInfoCommand answers /info.
func handleInfoCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	text := infoText()
	reply := payload.TgPlSendMessage{
		ChatId:                origChat.Id,
		ParseMode:             payload.ParseModeHTML,
		Text:                  text,
		ReplyToMessageId:      origMessage.MessageId,
		DisableNotification:   true,
		DisableWebPagePreview: true,
	}

	message.NewSendMessage(reply)
	log.Printf("ℹ️ Sent /info to user %d in chat %d", origUser.Id, origChat.Id)
}
