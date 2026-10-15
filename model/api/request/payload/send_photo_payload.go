package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

/*
@link https://core.telegram.org/bots/api#sendphoto
*/
type TgPlSendPhoto struct {
	ChatId              int64                         `json:"chat_id"`
	Photo               string                        `json:"photo"`
	Caption             string                        `json:"caption,omitempty"`
	ParseMode           string                        `json:"parse_mode,omitempty"`
	CaptionEntities     tg_api.TgPlaceholder          `json:"caption_entities,omitempty"`
	HasSpoiler          bool                          `json:"has_spoiler,omitempty"`
	DisableNotification bool                          `json:"disable_notification,omitempty"`
	ReplyToMessageId    int                           `json:"reply_to_message_id,omitempty"`
	ReplyMarkup         tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
	FilePath            string                        `json:"-"`
}

func (m *TgPlSendPhoto) SetCaption(caption string) {
	m.Caption = caption
}

func (m *TgPlSendPhoto) SetParseMode(mode string) {
	m.ParseMode = mode
}

func (m *TgPlSendPhoto) GetFilePath() string {
	return m.FilePath
}

func (m *TgPlSendPhoto) GetFileField() string {
	return "photo"
}
