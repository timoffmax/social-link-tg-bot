package response

import (
	tg_api "github.com/timoffmax/social-link-bot/model/api"
)

/*
@link https://core.telegram.org/bots/api#message
*/
type TgMessage struct {
	MessageId                     int                    `json:"message_id"`
	From                          TgUser                 `json:"from,omitempty"`
	SenderChat                    TgChat                 `json:"sender_chat,omitempty"`
	Date                          int                    `json:"date"`
	Chat                          TgChat                 `json:"chat"`
	ForwarderFrom                 TgUser                 `json:"forwarder_from,omitempty"`
	ForwardedFromChat             TgChat                 `json:"forwarded_from_chat,omitempty"`
	ForwardedFromMessageId        int                    `json:"forwarded_from_message_id,omitempty"`
	ForwardSignature              string                 `json:"forward_signature,omitempty"`
	ForwardSenderName             string                 `json:"forward_sender_name,omitempty"`
	ForwardDate                   int                    `json:"forward_date,omitempty"`
	ReplyToMessage                *TgMessage             `json:"reply_to_message,omitempty"`
	ViaBot                        TgUser                 `json:"via_bot,omitempty"`
	EditDate                      int                    `json:"edit_date,omitempty"`
	MediaGroupId                  string                 `json:"media_group_id,omitempty"`
	AuthorSignature               string                 `json:"author_signature,omitempty"`
	Text                          string                 `json:"text,omitempty"`
	Entities                      []TgMessageEntity      `json:"entities,omitempty"`
	Animation                     tg_api.TgPlaceholder   `json:"animation,omitempty"`
	Audio                         tg_api.TgPlaceholder   `json:"audio,omitempty"`
	Document                      tg_api.TgPlaceholder   `json:"document,omitempty"`
	Photo                         []tg_api.TgPlaceholder `json:"photo,omitempty"`
	Sticker                       tg_api.TgPlaceholder   `json:"sticker,omitempty"`
	Video                         tg_api.TgPlaceholder   `json:"video,omitempty"`
	VideoNote                     tg_api.TgPlaceholder   `json:"video_note,omitempty"`
	Voice                         tg_api.TgPlaceholder   `json:"voice,omitempty"`
	Caption                       string                 `json:"caption,omitempty"`
	CaptionEntities               []tg_api.TgPlaceholder `json:"caption_entities,omitempty"`
	Contact                       tg_api.TgPlaceholder   `json:"contact,omitempty"`
	Dice                          tg_api.TgPlaceholder   `json:"dice,omitempty"`
	Game                          tg_api.TgPlaceholder   `json:"game,omitempty"`
	Poll                          tg_api.TgPlaceholder   `json:"poll,omitempty"`
	Venue                         tg_api.TgPlaceholder   `json:"venue,omitempty"`
	Location                      tg_api.TgPlaceholder   `json:"location,omitempty"`
	OldChatMember                 TgUser                 `json:"old_chat_member,omitempty"`
	NewChatMember                 TgUser                 `json:"new_chat_member,omitempty"`
	NewChatMembers                []TgUser               `json:"new_chat_members,omitempty"`
	NewChatParticipant            TgUser                 `json:"new_chat_participant,omitempty"`
	LeftChatMember                TgUser                 `json:"left_chat_member,omitempty"`
	NewChatTitle                  string                 `json:"new_chat_title,omitempty"`
	NewChatPhoto                  []tg_api.TgPlaceholder `json:"new_chat_photo,omitempty"`
	DeleteChatPhoto               bool                   `json:"delete_chat_photo,omitempty"`
	GroupChatCreated              bool                   `json:"group_chat_created,omitempty"`
	SupergroupChatCreated         bool                   `json:"supergroup_chat_created,omitempty"`
	ChannelChatCreated            bool                   `json:"channel_chat_created,omitempty"`
	MessageAutoDeleteTimerChanged tg_api.TgPlaceholder   `json:"message_auto_delete_timer_changed,omitempty"`
	MigrateToChatId               int                    `json:"migrate_to_chat_id,omitempty"`
	MigrateFromChatId             int                    `json:"migrate_from_chat_id,omitempty"`
	PinnedMessage                 *TgMessage             `json:"pinned_message,omitempty"`
	Invoice                       tg_api.TgPlaceholder   `json:"invoice,omitempty"`
	SuccessfulPayment             tg_api.TgPlaceholder   `json:"successful_payment,omitempty"`
	ConnectedWebsite              string                 `json:"connected_website,omitempty"`
	PassportData                  tg_api.TgPlaceholder   `json:"passport_data,omitempty"`
	ProximityAlertTriggered       tg_api.TgPlaceholder   `json:"proximity_alert_triggered,omitempty"`
	VoiceChatScheduled            tg_api.TgPlaceholder   `json:"voice_chat_scheduled,omitempty"`
	VoiceChatStarted              tg_api.TgPlaceholder   `json:"voice_chat_started,omitempty"`
	VoiceChatEnded                tg_api.TgPlaceholder   `json:"voice_chat_ended,omitempty"`
	VoiceChatParticipantsInvited  tg_api.TgPlaceholder   `json:"voice_chat_participants_invited,omitempty"`
	ReplyMarkup                   tg_api.TgPlaceholder   `json:"reply_markup,omitempty"`
}

func (m *TgMessage) HasCommands() bool {
	result := false

	for _, entity := range m.Entities {
		result = result || (TypeBotCommand == entity.Type)
	}

	return result
}
