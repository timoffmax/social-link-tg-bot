package payload

type TgPlDeleteMessage struct {
	ChatId    int64 `json:"chat_id"`
	MessageId int   `json:"message_id"`
}