package api

/*
@link https://core.telegram.org/bots/api#inlinekeyboardbutton
*/
type TgInlineKeyboardButton struct {
	Text                         string        `json:"text"`
	URL                          string        `json:"url,omitempty"`
	LoginUrl                     TgPlaceholder `json:"login_url,omitempty"`
	CallbackData                 string        `json:"callback_data,omitempty"`
	SwitchInlineQuery            string        `json:"switch_inline_query,omitempty"`
	SwitchInlineQueryCurrentChat string        `json:"switch_inline_query_current_chat,omitempty"`
	CallbackGame                 TgPlaceholder `json:"callback_game,omitempty"`
	Pay                          bool          `json:"pay,omitempty"`
}
