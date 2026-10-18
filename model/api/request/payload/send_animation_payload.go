package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

/*
@link https://core.telegram.org/bots/api#sendanimation
*/
type TgPlSendAnimation struct {
	ChatId              int64                         `json:"chat_id"`
	Animation           string                        `json:"animation"`
	Duration            int                           `json:"duration,omitempty"`
	Width               int                           `json:"width,omitempty"`
	Height              int                           `json:"height,omitempty"`
	Caption             string                        `json:"caption,omitempty"`
	ParseMode           string                        `json:"parse_mode,omitempty"`
	HasSpoiler          bool                          `json:"has_spoiler,omitempty"`
	DisableNotification bool                          `json:"disable_notification,omitempty"`
	ReplyToMessageId    int                           `json:"reply_to_message_id,omitempty"`
	ReplyMarkup         tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
	FilePath            string                        `json:"-"`
}

// SetCaption sets the caption.
func (m *TgPlSendAnimation) SetCaption(caption string) {
	m.Caption = caption
}

// SetParseMode sets the caption's parse mode.
func (m *TgPlSendAnimation) SetParseMode(mode string) {
	m.ParseMode = mode
}

// GetFilePath returns the local file uploaded as the animation.
func (m *TgPlSendAnimation) GetFilePath() string {
	return m.FilePath
}

// GetFileField returns the multipart field of the uploaded file.
func (m *TgPlSendAnimation) GetFileField() string {
	return "animation"
}
