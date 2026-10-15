package payload

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

// CurrencyStars is the currency of payments in Telegram Stars.
const CurrencyStars = "XTR"

/*
@link https://core.telegram.org/bots/api#labeledprice
*/
type TgLabeledPrice struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
}

/*
For Telegram Stars: Currency "XTR", an empty ProviderToken and exactly one
price. Leave ReplyMarkup nil so Telegram adds the Pay button itself.

@link https://core.telegram.org/bots/api#sendinvoice
*/
type TgPlSendInvoice struct {
	ChatId              int64                         `json:"chat_id"`
	Title               string                        `json:"title"`
	Description         string                        `json:"description"`
	Payload             string                        `json:"payload"`
	ProviderToken       string                        `json:"provider_token"`
	Currency            string                        `json:"currency"`
	Prices              []TgLabeledPrice              `json:"prices"`
	StartParameter      string                        `json:"start_parameter,omitempty"`
	DisableNotification bool                          `json:"disable_notification,omitempty"`
	ReplyToMessageId    int                           `json:"reply_to_message_id,omitempty"`
	ReplyMarkup         tg_api.TgReplyMarkupInterface `json:"reply_markup,omitempty"`
}
