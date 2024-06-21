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
