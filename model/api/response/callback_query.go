package response

/*
@link https://core.telegram.org/bots/api#callbackquery
*/
type TgCallbackQuery struct {
	Id              string    `json:"id"`
	From            TgUser    `json:"from,omitempty"`
	Message         TgMessage `json:"message,omitempty"`
	InlineMessageId string    `json:"inline_message_id,omitempty"`
	ChatInstance    string    `json:"chat_instance"`
	Data            string    `json:"data,omitempty"`
	GameShortName   string    `json:"game_short_name,omitempty"`
}
