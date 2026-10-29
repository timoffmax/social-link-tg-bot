// Package reportreply holds the values of report replies that the bot (which
// sends them) and the dashboard (which queues them) must agree on. It has no
// code and no dependencies, so the dashboard shares no code path with the bot.
package reportreply

// Prefix opens every reply the bot sends to a reporter (Telegram HTML parse
// mode); the reply text follows it.
const Prefix = "💬 <b>Reply from the developer:</b>\n\n"

// MentionNameLimit caps, in runes, the name or @username that mentions the
// reporter above a reply posted in the report's group.
const MentionNameLimit = 32

// MentionReserve is the Telegram length the mention may take before the
// Prefix (the capped name, up to 2 units a rune, and ": "), so the dashboard
// leaves room for it.
const MentionReserve = (2 * MentionNameLimit) + 2

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
