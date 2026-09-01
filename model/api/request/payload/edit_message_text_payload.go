package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

type TgPlEditMessageText struct {
	ChatId      int64                         `json:"chat_id"`
	MessageId   int                           `json:"message_id"`
	Text        string                        `json:"text"`
	ParseMode   string                        `json:"parse_mode,omitempty"`
	ReplyMarkup tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
}
