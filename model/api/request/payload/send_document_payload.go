package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

/*
@link https://core.telegram.org/bots/api#senddocument
*/
type TgPlSendDocument struct {
	ChatId                      int64                         `json:"chat_id"`
	Document                    string                        `json:"document"`
	Thumb                       string                        `json:"thumb,omitempty"`
	Caption                     string                        `json:"caption,omitempty"`
	ParseMode                   string                        `json:"parse_mode,omitempty"`
	CaptionEntities             []tg_api.TgPlaceholder        `json:"caption_entities,omitempty"`
	DisableContentTypeDetection bool                          `json:"disable_content_type_detection,omitempty"`
	DisableNotification         bool                          `json:"disable_notification,omitempty"`
	ReplyToMessageId            int                           `json:"reply_to_message_id,omitempty"`
	ReplyMarkup                 tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
	FilePath                    string                        `json:"-"`
}

func (m *TgPlSendDocument) SetCaption(caption string) {
	m.Caption = caption
}

func (m *TgPlSendDocument) SetParseMode(mode string) {
	m.ParseMode = mode
}

func (m *TgPlSendDocument) GetFilePath() string {
	return m.FilePath
}

func (m *TgPlSendDocument) GetFileField() string {
	return "document"
}
