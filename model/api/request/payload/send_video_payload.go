package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

/*
@link https://core.telegram.org/bots/api#sendvideo
*/
type TgPlSendVideo struct {
	ChatId              int64                         `json:"chat_id"`
	Video               string                        `json:"video"`
	Duration            int                           `json:"duration,omitempty"`
	Width               int                           `json:"width,omitempty"`
	Height              int                           `json:"height,omitempty"`
	Thumb               string                        `json:"thumb,omitempty"`
	Caption             string                        `json:"caption,omitempty"`
	ParseMode           string                        `json:"parse_mode,omitempty"`
	CaptionEntities     tg_api.TgPlaceholder          `json:"caption_entities,omitempty"`
	SupportsStreaming   bool                          `json:"supports_streaming,omitempty"`
	DisableNotification bool                          `json:"disable_notification,omitempty"`
	ReplyToMessageId    int                           `json:"reply_to_message_id,omitempty"`
	ReplyMarkup         tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
	FilePath            string                        `json:"-"`
}

func (m *TgPlSendVideo) SetCaption(caption string) {
	m.Caption = caption
}

func (m *TgPlSendVideo) SetParseMode(mode string) {
	m.ParseMode = mode
}

func (m *TgPlSendVideo) GetFilePath() string {
	return m.FilePath
}

func (m *TgPlSendVideo) GetFileField() string {
	return "video"
}