package app

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// chatTypePrivate is the Telegram chat type of a one-to-one chat with the bot.
const chatTypePrivate = "private"

// /gated subcommands (owner only).
const (
	gatedActionStatus = "status"
	gatedActionOn     = "on"
	gatedActionOff    = "off"
	gatedActionCap    = "cap"
	gatedActionUser   = "user"

	// gatedLimitDefault resets a cap or allowance to the env default.
	gatedLimitDefault = "default"

	// gatedLimitUnlimited removes the chat's or user's own limit (the global
	// monthly cap still applies).
	gatedLimitUnlimited = "unlimited"
)

// gatedUsageText documents the /gated subcommands.
const gatedUsageText = `<b>Usage</b> (owner only):
• <code>/gated</code> or <code>/gated status [chat_id]</code>
• <code>/gated on|off [chat_id]</code>: allow paid fetches in a group
• <code>/gated cap N|unlimited|default [chat_id]</code>: group monthly cap
• reply to a user + <code>/gated user N|unlimited|default</code>, or <code>/gated user USER_ID N|unlimited|default</code>: private monthly allowance
The global monthly cap (GATED_FETCH_GLOBAL_MONTHLY_CAP) always applies.`

// telegramServiceUserID is the sender of auto-forwarded channel posts in a
// linked discussion group; it is not a real user.
const telegramServiceUserID = 777000

// /gated validation errors.
var (
	errGatedNotGroup  = errors.New("not a group chat id (group ids are negative); private chats use /gated user")
	errGatedNoUser    = errors.New("reply to the user's message or pass a user id")
	errGatedBotTarget = errors.New("the replied-to sender is a bot, channel or anonymous admin; pass a user id")
)

// gatedCommandHandler runs one /gated subcommand and returns the reply text.
type gatedCommandHandler func(params []string, origMessage *response.TgMessage, origChat *response.TgChat) string

// gatedCommandHandlers maps /gated subcommands to their handlers.
var gatedCommandHandlers = map[string]gatedCommandHandler{
	gatedActionStatus: handleGatedStatus,
	gatedActionOn:     handleGatedOn,
	gatedActionOff:    handleGatedOff,
	gatedActionCap:    handleGatedCap,
	gatedActionUser:   handleGatedUser,
}

// handleGatedCommand manages the paid fetch of author-gated Instagram posts.
// Non-owners are ignored silently, like the other owner commands.
func handleGatedCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	isAllowed := isOwner(origUser.Id)

	if isAllowed == false {
		log.Printf("🚫 Unauthorized /gated attempt by user %d", origUser.Id)
	}

	if isAllowed == true {
		text := runGatedCommand(origMessage, origChat)
		replyGated(origMessage, origChat, text)
	}
}

// runGatedCommand dispatches a /gated subcommand.
func runGatedCommand(origMessage *response.TgMessage, origChat *response.TgChat) string {
	result := fmt.Sprintf("❌ Unknown subcommand.\n\n%s", gatedUsageText)
	args := strings.Fields(origMessage.Text)
	action := gatedActionStatus
	var params []string

	if len(args) > 1 {
		action = strings.ToLower(args[1])
		params = args[2:]
	}

	handler, exists := gatedCommandHandlers[action]

	if exists == true {
		result = handler(params, origMessage, origChat)
	}

	return result
}

// handleGatedStatus shows the configuration and this month's usage, plus the
// policy of the current group or of the given group id.
func handleGatedStatus(params []string, origMessage *response.TgMessage, origChat *response.TgChat) string {
	var text strings.Builder
	chatID, err := parseGatedChatID(params, 0, origChat)
	hasExplicitChat := (len(params) > 0)
	globalUsed, usageErr := services.GetGatedFetchUsage(services.GatedFetchScopeGlobal, 0)
	globalCap := config.GetGatedFetchGlobalMonthlyCap()
	userAllowance := config.GetGatedFetchUserMonthlyAllowance()
	apifyToken := config.GetApifyToken()
	apifyActor := config.GetApifyInstagramActor()
	provider := "not configured (set APIFY_TOKEN)"

	if apifyToken != "" {
		provider = fmt.Sprintf("Apify (<code>%s</code>)", html.EscapeString(apifyActor))
	}

	if (err == nil) && (hasExplicitChat == true) && (chatID >= 0) {
		err = errGatedNotGroup
	}

	text.WriteString("🔞 <b>Gated Instagram fetch</b>\n\n")
	fmt.Fprintf(&text, "Provider: %s\n", provider)
	fmt.Fprintf(&text, "This month, all chats: <b>%d</b> of %d\n", globalUsed, globalCap)
	fmt.Fprintf(&text, "Private chats: %d free per user per month by default\n\n", userAllowance)

	if (err == nil) && (chatID < 0) {
		chatStatus := gatedChatStatusText(chatID)
		text.WriteString(chatStatus)
	}

	if err != nil {
		detail := gatedErrorText(err)
		fmt.Fprintf(&text, "❌ %s\n", detail)
	}

	if usageErr != nil {
		detail := gatedErrorText(usageErr)
		fmt.Fprintf(&text, "⚠️ Usage unavailable: %s\n", detail)
	}

	fmt.Fprintf(&text, "\n%s", gatedUsageText)
	result := text.String()

	return result
}

// gatedChatStatusText describes a group's policy and usage.
func gatedChatStatusText(chatID int64) string {
	policy, err := services.GetGatedFetchPolicy(services.GatedFetchScopeChat, chatID)
	used, usageErr := services.GetGatedFetchUsage(services.GatedFetchScopeChat, chatID)
	state := "❌ off"
	defaultCap := config.GetGatedFetchChatMonthlyCap()
	capText := fmt.Sprintf("%d (default)", defaultCap)
	result := ""

	if policy.Enabled == true {
		state = "✅ on"
	}

	if policy.MonthlyLimit.Valid == true {
		capText = formatGatedLimit(policy.MonthlyLimit.Int64)
	}

	result = fmt.Sprintf("Chat <code>%d</code>: %s, used <b>%d</b> of %s this month\n", chatID, state, used, capText)

	if (err != nil) || (usageErr != nil) {
		failure := firstGatedError(err, usageErr)
		detail := gatedErrorText(failure)
		result = fmt.Sprintf("❌ Failed to read chat <code>%d</code>: %s\n", chatID, detail)
	}

	return result
}

// handleGatedOn allows paid fetches in a group.
func handleGatedOn(params []string, origMessage *response.TgMessage, origChat *response.TgChat) string {
	result := setGatedChatEnabled(params, origChat, true)

	return result
}

// handleGatedOff denies paid fetches in a group.
func handleGatedOff(params []string, origMessage *response.TgMessage, origChat *response.TgChat) string {
	result := setGatedChatEnabled(params, origChat, false)

	return result
}

// setGatedChatEnabled stores a group's on/off switch.
func setGatedChatEnabled(params []string, origChat *response.TgChat, enabled bool) string {
	chatID, err := parseGatedGroupID(params, 0, origChat)

	if err == nil {
		err = services.SetGatedFetchEnabled(services.GatedFetchScopeChat, chatID, enabled)
	}

	result := gatedResultText(err, chatID, gatedChatStatusText)

	return result
}

// handleGatedCap stores a group's monthly cap.
func handleGatedCap(params []string, origMessage *response.TgMessage, origChat *response.TgChat) string {
	limit, err := parseGatedLimit(params, 0)
	chatID := int64(0)

	if err == nil {
		chatID, err = parseGatedGroupID(params, 1, origChat)
	}

	if err == nil {
		err = services.SetGatedFetchMonthlyLimit(services.GatedFetchScopeChat, chatID, limit)
	}

	result := gatedResultText(err, chatID, gatedChatStatusText)

	return result
}

// handleGatedUser stores a user's private monthly allowance, targeting the
// replied-to user or an explicit user id.
func handleGatedUser(params []string, origMessage *response.TgMessage, origChat *response.TgChat) string {
	userID, limitParams, err := parseGatedUserTarget(params, origMessage)
	limit := sql.NullInt64{}

	if err == nil {
		limit, err = parseGatedLimit(limitParams, 0)
	}

	if err == nil {
		err = services.SetGatedFetchMonthlyLimit(services.GatedFetchScopeUser, userID, limit)
	}

	result := gatedResultText(err, userID, gatedUserStatusText)

	return result
}

// gatedUserStatusText describes a user's allowance and usage.
func gatedUserStatusText(userID int64) string {
	policy, err := services.GetGatedFetchPolicy(services.GatedFetchScopeUser, userID)
	used, usageErr := services.GetGatedFetchUsage(services.GatedFetchScopeUser, userID)
	defaultAllowance := config.GetGatedFetchUserMonthlyAllowance()
	allowance := fmt.Sprintf("%d (default)", defaultAllowance)

	if policy.MonthlyLimit.Valid == true {
		allowance = formatGatedLimit(policy.MonthlyLimit.Int64)
	}

	result := fmt.Sprintf("User <code>%d</code>: used <b>%d</b> of %s this month\n", userID, used, allowance)

	if (err != nil) || (usageErr != nil) {
		failure := firstGatedError(err, usageErr)
		detail := gatedErrorText(failure)
		result = fmt.Sprintf("❌ Failed to read user <code>%d</code>: %s\n", userID, detail)
	}

	return result
}

// parseGatedUserTarget returns the target user and the remaining params: an
// explicit id (params = [id, limit]) wins; otherwise the replied-to user
// (params = [limit]). Bots (incl. anonymous admins and channels) are rejected.
func parseGatedUserTarget(params []string, origMessage *response.TgMessage) (int64, []string, error) {
	userID := int64(0)
	rest := params
	var err error
	hasExplicitID := (len(params) >= 2)
	replied := origMessage.ReplyToMessage
	hasReply := ((replied != nil) && (replied.From.Id != 0))

	if hasExplicitID == true {
		userID, err = strconv.ParseInt(params[0], 10, 64)
		rest = params[1:]
	}

	isServiceSender := ((hasReply == true) && ((replied.From.IsBot == true) || (replied.From.Id == telegramServiceUserID)))

	if (hasExplicitID == false) && (isServiceSender == true) {
		err = errGatedBotTarget
	}

	if (hasExplicitID == false) && (hasReply == true) && (err == nil) {
		userID = int64(replied.From.Id)
	}

	if (err == nil) && (userID <= 0) {
		err = errGatedNoUser
	}

	return userID, rest, err
}

// parseGatedChatID returns params[index] as a chat id, or the current chat.
func parseGatedChatID(params []string, index int, origChat *response.TgChat) (int64, error) {
	result := origChat.Id
	var err error

	if len(params) > index {
		result, err = strconv.ParseInt(params[index], 10, 64)
	}

	if err != nil {
		err = fmt.Errorf("invalid chat id: %s", params[index])
	}

	return result, err
}

// parseGatedGroupID is parseGatedChatID restricted to groups (negative ids):
// private chats use the per-user allowance instead.
func parseGatedGroupID(params []string, index int, origChat *response.TgChat) (int64, error) {
	result, err := parseGatedChatID(params, index, origChat)

	if (err == nil) && (result >= 0) {
		err = errGatedNotGroup
	}

	return result, err
}

// parseGatedLimit parses params[index] as a non-negative limit, "unlimited"
// (GatedFetchUnlimited), or "default" (an invalid NullInt64) to reset to the
// env default.
func parseGatedLimit(params []string, index int) (sql.NullInt64, error) {
	result := sql.NullInt64{}
	err := fmt.Errorf("missing limit: a number, %s or %s", gatedLimitUnlimited, gatedLimitDefault)
	raw := ""

	if len(params) > index {
		raw = strings.ToLower(params[index])
		err = nil
	}

	isKeyword := ((raw == gatedLimitDefault) || (raw == gatedLimitUnlimited))

	if raw == gatedLimitUnlimited {
		result = sql.NullInt64{Int64: services.GatedFetchUnlimited, Valid: true}
	}

	if (err == nil) && (isKeyword == false) {
		result, err = parseGatedLimitNumber(raw)
	}

	return result, err
}

// formatGatedLimit renders a stored limit for status replies.
func formatGatedLimit(limit int64) string {
	result := fmt.Sprintf("%d", limit)

	if limit == services.GatedFetchUnlimited {
		result = gatedLimitUnlimited
	}

	return result
}

// parseGatedLimitNumber parses a non-negative integer limit.
func parseGatedLimitNumber(raw string) (sql.NullInt64, error) {
	parsed, err := strconv.ParseInt(raw, 10, 64)
	result := sql.NullInt64{Int64: parsed, Valid: true}

	if (err != nil) || (parsed < 0) {
		err = fmt.Errorf("invalid limit: %s", raw)
	}

	return result, err
}

// gatedResultText returns the status of the changed chat or user, or the
// error with usage. The status is only read after a successful change.
func gatedResultText(err error, targetID int64, statusText func(int64) string) string {
	result := ""

	if err == nil {
		status := statusText(targetID)
		result = fmt.Sprintf("✅ Saved.\n\n%s", status)
	}

	if err != nil {
		detail := gatedErrorText(err)
		result = fmt.Sprintf("❌ %s\n\n%s", detail, gatedUsageText)
	}

	return result
}

// firstGatedError returns the first non-nil error (the server builds with Go
// 1.17, which has no errors.Join).
func firstGatedError(errs ...error) error {
	var result error

	for _, err := range errs {
		if (result == nil) && (err != nil) {
			result = err
		}
	}

	return result
}

// gatedErrorText escapes an error for an HTML reply.
func gatedErrorText(err error) string {
	detail := err.Error()
	result := html.EscapeString(detail)

	return result
}

// replyGated sends a /gated reply.
func replyGated(origMessage *response.TgMessage, origChat *response.TgChat, text string) {
	reply := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                text,
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}

	message.NewSendMessage(reply)
}
