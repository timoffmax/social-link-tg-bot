package app

import (
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/model/api/response/utils"
)

func listenForUpdates() {
	var offset int

	for {
		lastProcessedUpd := processRecentUpdates(offset)

		if nil != lastProcessedUpd {
			offset = lastProcessedUpd.UpdateId + 1
		}
	}
}

func processRecentUpdates(offset int) *response.TgUpdate {
	var lastProcessedUpd *response.TgUpdate
	updates := getRecentUpdates(offset)

	for _, update := range updates {
		processUpdate(update)
		lastProcessedUpd = update
	}

	return lastProcessedUpd
}

func getRecentUpdates(offset int) []*response.TgUpdate {
	updates := message.NewGetUpdates(payload.TgPlGetUpdates{
		Offset:         offset,
		AllowedUpdates: []string{"message"},
	})

	respPayload := updates.ResponsePayload
	result := respPayload.Items

	return result
}

func processUpdate(upd *response.TgUpdate) {
	var origMessage response.TgMessage
	var origUser response.TgUser
	var origChat response.TgChat

	if 0 != upd.Message.MessageId {
		origMessage = upd.Message
		origUser = origMessage.From
		origChat = origMessage.Chat
	}

	if 0 == origUser.Id {
		return
	}

	fixedLinks := utils.GetAllLinks(&origMessage)

	for _, fixedUrl := range fixedLinks {
		requestPayload := payload.TgPlSendMessage{
			ChatId:    origChat.Id,
			ParseMode: payload.ParseModeHTML,
		}

		requestPayload.Text = fixedUrl

		message.NewSendMessage(requestPayload)
	}
}
