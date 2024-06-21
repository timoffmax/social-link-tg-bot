package response

const TypeBotCommand = "bot_command"

/*
@link https://core.telegram.org/bots/api#messageentity
*/
type TgMessageEntity struct {
	Type     string  `json:"type"`
	Offset   int     `json:"offset"`
	Length   int     `json:"length"`
	URL      string  `json:"url,omitempty"`
	User     *TgUser `json:"user,omitempty"`
	Language string  `json:"language,omitempty"`
}
