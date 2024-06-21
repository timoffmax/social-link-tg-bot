package response

import (
	"encoding/json"
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

/*
*
An array of struct od type @see TgUpdate
*/
type TgUpdates struct {
	Items []*TgUpdate
}

/*
@link https://core.telegram.org/bots/api#update
*/
type TgUpdate struct {
	UpdateId           int                  `json:"update_id"`
	Message            TgMessage            `json:"message"`
	EditedMessage      TgMessage            `json:"edited_message"`
	ChannelPost        TgMessage            `json:"channel_post"`
	EditedChannelPost  TgMessage            `json:"edited_channel_post"`
	InlineQuery        tg_api.TgPlaceholder `json:"inline_query"`
	ChosenInlineResult tg_api.TgPlaceholder `json:"chosen_inline_result"`
	CallbackQuery      TgCallbackQuery      `json:"callback_query"`
	ShippingQuery      tg_api.TgPlaceholder `json:"shipping_query"`
	PreCheckoutQuery   tg_api.TgPlaceholder `json:"pre_checkout_query"`
	Poll               tg_api.TgPlaceholder `json:"poll"`
	PollAnswer         tg_api.TgPlaceholder `json:"poll_answer"`
	MyChatMember       tg_api.TgPlaceholder `json:"my_chat_member"`
	ChatMember         tg_api.TgPlaceholder `json:"chat_member"`
}

func (r *TgUpdates) UnmarshalJSON(body []byte) error {
	var items []*TgUpdate

	if err := json.Unmarshal(body, &items); err != nil {
		return err
	}

	r.Items = items

	return nil
}
