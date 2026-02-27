package payload

const (
	ChatActionTyping          = "typing"
	ChatActionUploadPhoto     = "upload_photo"
	ChatActionUploadVideo     = "upload_video"
	ChatActionUploadDocument  = "upload_document"
	ChatActionUploadVideoNote = "upload_video_note"
)

// TgPlSendChatAction represents the payload for the sendChatAction API method.
// @link https://core.telegram.org/bots/api#sendchataction
type TgPlSendChatAction struct {
	ChatId int64  `json:"chat_id"`
	Action string `json:"action"`
}
