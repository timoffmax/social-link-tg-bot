package api

type TgReplyMarkupInterface interface {
	isReplyMarkup() bool
}

/*
@link https://core.telegram.org/bots/api#inlinekeyboardmarkup
*/
type TgInlineKeyboardMarkup struct {
	InlineKeyboard [][]TgInlineKeyboardButton `json:"inline_keyboard"`
}

/*
*
Is not really going to be use
Just a flag that allows to use the corresponding general
*/
func (m *TgInlineKeyboardMarkup) isReplyMarkup() bool {
	return true
}

/*
@link https://core.telegram.org/bots/api#forcereply
*/
type TgForceReply struct {
	ForceReply            bool   `json:"force_reply"`
	InputFieldPlaceholder string `json:"input_field_placeholder,omitempty"`
	Selective             bool   `json:"selective,omitempty"`
}

// isReplyMarkup marks TgForceReply as a reply markup.
func (m *TgForceReply) isReplyMarkup() bool {
	return true
}
