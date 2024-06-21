package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

const ParseModeHTML = "HTML"

/*
@link https://core.telegram.org/bots/api#sendmessage
*/
type TgPlSendMessage struct {
	ChatId                   int64                         `json:"chat_id"`
	Text                     string                        `json:"text"`
	ParseMode                string                        `json:"parse_mode,omitempty"`
	Entities                 tg_api.TgPlaceholder          `json:"entities,omitempty"`
	DisableWebPagePreview    bool                          `json:"disable_web_page_preview,omitempty"`
	DisableNotification      bool                          `json:"disable_notification,omitempty"`
	ReplyToMessageId         int                           `json:"reply_to_message_id,omitempty"`
	AllowSendingWithoutReply bool                          `json:"allow_sending_without_reply,omitempty"`
	ReplyMarkup              tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
}

func (m *TgPlSendMessage) SetText(text string) {
	m.Text = text
}

func (m *TgPlSendMessage) SetButtons(buttons tg_api.TgReplyMarkupInterface) {
	m.ReplyMarkup = buttons
}

func (m *TgPlSendMessage) SetParseMode(mode string) {
	m.ParseMode = mode
}
