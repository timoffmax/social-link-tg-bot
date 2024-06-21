package response

import tg_api "github.com/timoffmax/social-link-bot/model/api"

/*
@link https://core.telegram.org/bots/api#chat
*/
type TgChat struct {
	Id                    int64                `json:"id"`
	Type                  string               `json:"type"`
	Title                 string               `json:"title,omitempty"`
	Username              string               `json:"username,omitempty"`
	FirstName             string               `json:"first_name,omitempty"`
	LastName              string               `json:"last_name,omitempty"`
	Photo                 tg_api.TgPlaceholder `json:"photo,omitempty"`
	Bio                   string               `json:"bio,omitempty"`
	Description           string               `json:"description,omitempty"`
	InviteLink            string               `json:"invite_link,omitempty"`
	PinnedMessage         *TgMessage           `json:"pinned_message,omitempty"`
	Permissions           tg_api.TgPlaceholder `json:"permissions,omitempty"`
	SlowModeDelay         int                  `json:"slow_mode_delay,omitempty"`
	MessageAutoDeleteTime int                  `json:"message_auto_delete_time,omitempty"`
	StickerSetName        string               `json:"sticker_set_name,omitempty"`
	CanSetStickerSet      bool                 `json:"can_set_sticker_set,omitempty"`
	LinkedChatId          int                  `json:"linked_chat_id,omitempty"`
	Location              tg_api.TgPlaceholder `json:"location,omitempty"`
}
