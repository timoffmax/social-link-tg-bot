package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

/*
@link https://core.telegram.org/bots/api#sendmediagroup
*/
type TgPlSendMediaGroup struct {
	ChatId              int64                         `json:"chat_id"`
	Media               []InputMediaPhoto             `json:"media"`
	DisableNotification bool                          `json:"disable_notification,omitempty"`
	ReplyToMessageId    int                           `json:"reply_to_message_id,omitempty"`
	ReplyMarkup         tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
	FilePaths           []string                      `json:"-"`
}

type InputMediaPhoto struct {
	Type      string `json:"type"`
	Media     string `json:"media"`
	Caption   string `json:"caption,omitempty"`
	ParseMode string `json:"parse_mode,omitempty"`
}

func (m *TgPlSendMediaGroup) GetFilePaths() []string {
	return m.FilePaths
}

func (m *TgPlSendMediaGroup) GetFileField() string {
	return "media"
}