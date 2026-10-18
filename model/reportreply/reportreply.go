// Package reportreply holds the values of report replies that the bot (which
// sends them) and the dashboard (which queues them) must agree on. It has no
// code and no dependencies, so the dashboard shares no code path with the bot.
package reportreply

// Prefix opens every reply the bot sends to a reporter (Telegram HTML parse
// mode); the reply text follows it.
const Prefix = "💬 <b>Reply from the developer:</b>\n\n"

// Sources of a report reply.
const (
	SourceDashboard = "dashboard"
	SourceTelegram  = "telegram"
)

// Statuses of a report reply (report_replies.status). Queued waits for the
// bot's outbox; sending was claimed by a sender and is never sent again
// automatically; sent reached the user; failed was refused by Telegram or
// never left the bot; unknown got no answer from Telegram, so it may have
// been delivered.
const (
	StatusQueued  = "queued"
	StatusSending = "sending"
	StatusSent    = "sent"
	StatusFailed  = "failed"
	StatusUnknown = "unknown"
)
