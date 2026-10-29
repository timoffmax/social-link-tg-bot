package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/reportreply"
	"github.com/timoffmax/social-link-bot/services"
)

// reportReplyOutboxInterval is how often the outbox looks for queued replies.
const reportReplyOutboxInterval = 5 * time.Second

// reportReplyOutboxBatch caps the replies sent in one outbox pass.
const reportReplyOutboxBatch = 20

// reportReplyNoMessageIDError is the reason of an answer that reports
// success without a message id: Telegram took the message, so it counts as
// delivery unknown, not as a refusal.
const reportReplyNoMessageIDError = "Telegram answered without a message id"

// reportReplyMaxAttempts caps the sends of one reply that Telegram refuses
// for a passing reason (429, 5xx); after the last one it is failed.
const reportReplyMaxAttempts = 5

// reportReplyRetryDelay is the wait before the next attempt, times the
// attempts so far, when Telegram does not say how long to wait.
const reportReplyRetryDelay = 30 * time.Second

// reportReplyGaveUp ends the reason of a reply failed after its last
// attempt: reason, attempts.
const reportReplyGaveUp = "%s (gave up after %d attempts)"

// reportReplyFailedAlert tells the owner a dashboard reply was not delivered:
// reply id, report id, user id, reason.
const reportReplyFailedAlert = "⚠️ Dashboard reply #%d to report #%d (user %d) was not sent: %s"

// reportReplyUnknownAlert tells the owner a dashboard reply got no answer from
// Telegram, so it may have reached the user: reply id, report id, user id,
// reason.
const reportReplyUnknownAlert = "⚠️ Dashboard reply #%d to report #%d (user %d) may have been delivered, not resent: %s"

// reportReplyDeliveryUnknown starts the reason of a send without an answer.
const reportReplyDeliveryUnknown = "delivery unknown (no answer from Telegram)"

// reportReplyMarkSentAttempts is how often a delivered reply's 'sent' write is
// tried: the reply already reached the user, so a lost write would show it as
// delivery unknown once its claim is old.
const reportReplyMarkSentAttempts = 3

// reportReplyMarkSentRetryDelay is the pause between those attempts.
const reportReplyMarkSentRetryDelay = time.Second

// replyDeliveryUnknownError is a send that got no answer from Telegram
// (timeout, dropped connection): Telegram may or may not have received it.
type replyDeliveryUnknownError struct {
	cause error
}

func (e replyDeliveryUnknownError) Error() string {
	result := fmt.Sprintf("%s: %v", reportReplyDeliveryUnknown, e.cause)

	return result
}

func (e replyDeliveryUnknownError) Unwrap() error {
	return e.cause
}

// replyRetryableError is an answered refusal for a passing reason (429 Too
// Many Requests, a 5xx): the reply was not delivered and may be sent again
// after retryAfter (0 when Telegram did not say).
type replyRetryableError struct {
	cause         error
	retryAfter    time.Duration
	isRateLimited bool
}

func (e replyRetryableError) Error() string {
	result := e.cause.Error()

	return result
}

func (e replyRetryableError) Unwrap() error {
	return e.cause
}

// reportReplyOutboxWg lets Run wait for the outbox's current reply before the
// database closes.
var reportReplyOutboxWg sync.WaitGroup

// Report reply store and alert functions; variables so tests run without a
// database or a real admin chat.
var (
	claimNextReportReply         = services.ClaimNextQueuedReportReply
	markReportReplySent          = services.MarkReportReplySent
	markReportReplyFailed        = services.MarkReportReplyFailed
	markReportReplyUnknown       = services.MarkReportReplyUnknown
	requeueReportReply           = services.RequeueReportReply
	markInterruptedReportReplies = services.MarkInterruptedReportReplies
	createReportReply            = services.CreateReportReply
	getReport                    = services.GetReport
	resolveReport                = services.ResolveReport
	notifyOwner                  = services.NotifyAdminUnthrottled
)

// startReportReplyOutbox sends the replies queued from the dashboard until ctx
// is done.
func startReportReplyOutbox(ctx context.Context) {
	if services.IsDBInitialized() == true {
		reportReplyOutboxWg.Add(1)
		go runReportReplyOutbox(ctx)
		log.Println("Started report reply outbox")
	}

	if services.IsDBInitialized() == false {
		log.Println("Report reply outbox not started: no database")
	}
}

// waitForReportReplyOutbox waits until the outbox has stopped, so a reply in
// flight is recorded before the database closes.
func waitForReportReplyOutbox() {
	reportReplyOutboxWg.Wait()
}

// runReportReplyOutbox is the outbox loop; it stops once ctx is done, after
// the reply in flight. Every pass first marks replies stuck in 'sending' as
// delivery unknown: only claims older than services.ReportReplyStuckAfter,
// so the outbox's own sends (one at a time, finished before the check) and
// /reply sends still waiting for Telegram are never among them.
func runReportReplyOutbox(ctx context.Context) {
	defer reportReplyOutboxWg.Done()
	markInterruptedReplies()
	isRunning := true
	ticker := time.NewTicker(reportReplyOutboxInterval)
	defer ticker.Stop()

	for isRunning == true {
		select {
		case <-ctx.Done():
			isRunning = false
			log.Println("Report reply outbox stopped")
		case <-ticker.C:
			markInterruptedReplies()
			sendQueuedReportReplies(ctx)
		}
	}
}

// markInterruptedReplies marks replies stuck in 'sending' as delivery
// unknown and tells the owner about the dashboard ones.
func markInterruptedReplies() {
	replies, err := markInterruptedReportReplies()

	if err != nil {
		log.Printf("❌ Failed to mark interrupted report replies: %v", err)
	}

	for _, reply := range replies {
		log.Printf("⚠️ Report reply #%d (report #%d) was interrupted while sending, may have been delivered, not resent", reply.ID, reply.ReportID)
		notifyInterruptedReply(reply)
	}
}

// notifyInterruptedReply tells the owner about an interrupted dashboard reply.
func notifyInterruptedReply(reply services.ReportReply) {
	var userID int64

	if reply.Source == services.ReportReplySourceDashboard {
		report, err := getReport(reply.ReportID)

		if err == nil {
			userID = report.UserID
		}

		notifyReplyFailed(reply, userID, reportReplyUnknownAlert, services.ReportReplyInterruptedError)
	}
}

// sendQueuedReportReplies sends queued replies, oldest first, until none is
// due, the batch is full, Telegram asks to slow down (429) or ctx is done.
func sendQueuedReportReplies(ctx context.Context) int {
	result := 0
	isClaimed := true
	isRateLimited := false

	for (isClaimed == true) && (isRateLimited == false) && (result < reportReplyOutboxBatch) && (ctx.Err() == nil) {
		isClaimed, isRateLimited = sendNextReportReply()

		if isClaimed == true {
			result++
		}
	}

	return result
}

// sendNextReportReply claims and sends the oldest queued reply. It returns
// whether a reply was claimed and whether Telegram rate-limited the send.
func sendNextReportReply() (bool, bool) {
	claimed, isClaimed, err := claimNextReportReply()
	isRateLimited := false

	if err != nil {
		log.Printf("❌ Failed to claim a queued report reply: %v", err)
	}

	if isClaimed == true {
		isRateLimited = deliverQueuedReply(claimed)
	}

	return isClaimed, isRateLimited
}

// deliverQueuedReply sends a claimed reply and records the outcome: sent
// (the store resolves the report when the reply asked for it), back in the
// queue after a passing refusal, or failed / delivery unknown. It returns
// whether Telegram rate-limited the send.
func deliverQueuedReply(claimed services.ClaimedReportReply) bool {
	reply := claimed.Reply
	target := claimedReplyTarget(claimed)
	delivery, sendErr := sendReplyToReporter(target, reply.Text)
	var retryErr replyRetryableError
	isRetryable := errors.As(sendErr, &retryErr)
	attempt := reply.Attempts + 1
	canRetry := (isRetryable == true) && (attempt < reportReplyMaxAttempts)
	result := (isRetryable == true) && (retryErr.isRateLimited == true)

	if sendErr == nil {
		recordReplySent(reply.ID, delivery)
		log.Printf("💬 Dashboard reply #%d sent to user %d (report #%d, in the group: %t)", reply.ID, claimed.UserID, reply.ReportID, delivery.IsInGroup)
	}

	if canRetry == true {
		requeueReply(reply, retryErr, attempt)
	}

	if (sendErr != nil) && (canRetry == false) {
		reason := undeliveredReason(sendErr, isRetryable, attempt)
		recordReplyUndelivered(claimed, sendErr, reason)
	}

	return result
}

// undeliveredReason is the stored reason of a reply that is not sent again;
// a passing refusal on the last attempt says the bot gave up.
func undeliveredReason(sendErr error, isRetryable bool, attempt int) string {
	result := sendErr.Error()

	if isRetryable == true {
		result = fmt.Sprintf(reportReplyGaveUp, result, attempt)
	}

	return result
}

// requeueReply puts a reply refused for a passing reason back in the queue
// until the wait Telegram asked for (or the backoff) has passed.
func requeueReply(reply services.ReportReply, retryErr replyRetryableError, attempt int) {
	delay := replyRetryDelay(retryErr, attempt)
	notBefore := time.Now().Add(delay)
	reason := retryErr.Error()
	err := requeueReportReply(reply.ID, reason, notBefore)

	if err != nil {
		log.Printf("❌ Failed to put report reply #%d back in the queue: %v", reply.ID, err)
	}

	log.Printf("⏳ Dashboard reply #%d (report #%d) refused for now (attempt %d of %d), retrying in %s: %s", reply.ID, reply.ReportID, attempt, reportReplyMaxAttempts, delay, reason)
}

// replyRetryDelay is Telegram's retry_after, or the backoff for the attempt.
func replyRetryDelay(retryErr replyRetryableError, attempt int) time.Duration {
	result := reportReplyRetryDelay * time.Duration(attempt)

	if retryErr.retryAfter > 0 {
		result = retryErr.retryAfter
	}

	return result
}

// recordReplyUndelivered stores a reply that is not sent again, as failed or
// delivery unknown, and tells the owner.
func recordReplyUndelivered(claimed services.ClaimedReportReply, sendErr error, reason string) {
	reply := claimed.Reply
	isUnknown := isReplyDeliveryUnknown(sendErr)
	alert := reportReplyFailedAlert

	if isUnknown == true {
		recordReplyUnknown(reply.ID, reason)
		alert = reportReplyUnknownAlert
	}

	if isUnknown == false {
		recordReplyFailed(reply.ID, reason)
	}

	log.Printf("❌ Dashboard reply #%d to user %d (report #%d) not delivered: %s", reply.ID, claimed.UserID, reply.ReportID, reason)
	notifyReplyFailed(reply, claimed.UserID, alert, reason)
}

// isReplyDeliveryUnknown reports whether a send got no answer from Telegram.
func isReplyDeliveryUnknown(sendErr error) bool {
	var unknown replyDeliveryUnknownError
	result := errors.As(sendErr, &unknown)

	return result
}

// notifyReplyFailed tells the owner a dashboard reply was not delivered, with
// alert as the format (reply id, report id, user id, reason).
func notifyReplyFailed(reply services.ReportReply, userID int64, alert string, reason string) {
	text := fmt.Sprintf(alert, reply.ID, reply.ReportID, userID, reason)
	isSent := notifyOwner(text)

	if isSent == false {
		log.Printf("❌ Could not tell the owner that reply #%d failed", reply.ID)
	}
}

// recordReplySent stores a delivered reply's message id and where it went,
// retrying a failed write a few times.
func recordReplySent(replyID int, delivery reportReplyDelivery) {
	telegramMessageID := int64(delivery.MessageID)
	err := markReportReplySent(replyID, telegramMessageID, delivery.IsInGroup)
	attempt := 1

	for (err != nil) && (attempt < reportReplyMarkSentAttempts) {
		log.Printf("⚠️ Failed to mark report reply #%d sent (attempt %d), retrying: %v", replyID, attempt, err)
		time.Sleep(reportReplyMarkSentRetryDelay)
		err = markReportReplySent(replyID, telegramMessageID, delivery.IsInGroup)
		attempt++
	}

	if err != nil {
		log.Printf("❌ Failed to mark report reply #%d sent: %v", replyID, err)
	}
}

// recordReplyFailed stores why a reply was definitely not delivered.
func recordReplyFailed(replyID int, reason string) {
	err := markReportReplyFailed(replyID, reason)

	if err != nil {
		log.Printf("❌ Failed to mark report reply #%d failed: %v", replyID, err)
	}
}

// recordReplyUnknown stores a reply whose delivery is unknown.
func recordReplyUnknown(replyID int, reason string) {
	err := markReportReplyUnknown(replyID, reason)

	if err != nil {
		log.Printf("❌ Failed to mark report reply #%d delivery unknown: %v", replyID, err)
	}
}

// reportReplyNotStartedError is Telegram's refusal of a private message to a
// user who never started the bot.
const reportReplyNotStartedError = "bot can't initiate conversation with a user"

// reportReplyGroupError joins the private chat's refusal and the group's
// error: private reason, group error.
const reportReplyGroupError = "%v; in the group: %w"

// reportReplyTarget is where a reply to a report goes: the reporter's private
// chat, else the report's group (ChatID) as a reply to ReplyToMessageID.
type reportReplyTarget struct {
	UserID           int64
	Username         string
	FirstName        string
	ChatID           int64
	ReplyToMessageID int
}

// reportReplyDelivery is a delivered reply: its message and whether it went
// to the report's group.
type reportReplyDelivery struct {
	MessageID int
	IsInGroup bool
}

// claimedReplyTarget is the target of a reply claimed by the outbox.
func claimedReplyTarget(claimed services.ClaimedReportReply) reportReplyTarget {
	result := reportReplyTarget{
		UserID:           claimed.UserID,
		Username:         claimed.Username,
		FirstName:        claimed.FirstName,
		ChatID:           claimed.ChatID,
		ReplyToMessageID: claimed.ReplyToMessageID,
	}

	return result
}

// reportTargetOf is the target of a reply to the report.
func reportTargetOf(report *services.Report) reportReplyTarget {
	result := reportReplyTarget{
		UserID:           report.UserID,
		Username:         report.Username,
		FirstName:        report.FirstName,
		ChatID:           report.ChatID,
		ReplyToMessageID: report.ReplyToMessageID,
	}

	return result
}

// sendReplyToReporter sends a reply to the reporter's private chat. When the
// reporter never started the bot and reported from a group, it goes to that
// group instead, as a reply to the report's message, mentioning the reporter.
// The error carries Telegram's description (e.g. "Forbidden: bot was blocked
// by the user"), or the transport error when there was no answer; a failed
// group send keeps the group error's kind, after the private chat's refusal.
func sendReplyToReporter(target reportReplyTarget, text string) (reportReplyDelivery, error) {
	var result reportReplyDelivery
	privateMessage := privateReplyMessage(target, text)
	messageID, err := sendReportReplyMessage(privateMessage)
	result.MessageID = messageID
	privateErr := err
	isGroupNeeded := (isNotStartedRefusal(err) == true) && (isGroupReport(target) == true)

	if isGroupNeeded == true {
		groupMessage := groupReplyMessage(target, text)
		result.MessageID, err = sendReportReplyMessage(groupMessage)
		result.IsInGroup = (err == nil)
		log.Printf("💬 User %d never started the bot, report reply sent in the group %d: %t", target.UserID, target.ChatID, result.IsInGroup)
	}

	if (isGroupNeeded == true) && (err != nil) {
		err = fmt.Errorf(reportReplyGroupError, privateErr, err)
	}

	return result, err
}

// privateReplyMessage is the reply in the reporter's private chat.
func privateReplyMessage(target reportReplyTarget, text string) payload.TgPlSendMessage {
	result := payload.TgPlSendMessage{
		ChatId:    target.UserID,
		ParseMode: payload.ParseModeHTML,
		Text:      services.ReportReplyPrefix + text,
	}

	return result
}

// groupReplyMessage is the reply in the report's group: it mentions the
// reporter and answers the report's message (sent without it when that
// message is gone or unknown).
func groupReplyMessage(target reportReplyTarget, text string) payload.TgPlSendMessage {
	mention := reporterMention(target)
	result := payload.TgPlSendMessage{
		ChatId:                   target.ChatID,
		ParseMode:                payload.ParseModeHTML,
		Text:                     fmt.Sprintf("%s%s%s", mention, services.ReportReplyPrefix, text),
		ReplyToMessageId:         target.ReplyToMessageID,
		AllowSendingWithoutReply: true,
	}

	return result
}

// reporterMention mentions the reporter by user id, so a username given up
// since the report can't point at someone else; it shows the @username, else
// the name cut to reportreply.MentionNameLimit runes with the ellipsis (the
// dashboard keeps room for it).
func reporterMention(target reportReplyTarget) string {
	shown := shortenRunes(target.FirstName, reportreply.MentionNameLimit-1)

	if target.Username != "" {
		shown = fmt.Sprintf("@%s", target.Username)
	}

	result := userLinkMention(target.UserID, shown)

	return result
}

// sendReportReplyMessage sends a reply message and returns its message id.
func sendReportReplyMessage(msg payload.TgPlSendMessage) (int, error) {
	var result int
	sent := message.NewSendMessage(msg)
	err := telegramSendError(sent)

	if err == nil {
		result = sent.ResponsePayload.MessageId
	}

	return result, err
}

// isNotStartedRefusal reports Telegram's refusal of a private message to a
// user who never started the bot.
func isNotStartedRefusal(err error) bool {
	result := (err != nil) && (strings.Contains(err.Error(), reportReplyNotStartedError) == true)

	return result
}

// isGroupReport reports a report filed in a group, not in a private chat.
func isGroupReport(target reportReplyTarget) bool {
	result := (target.ChatID != 0) && (target.ChatID != target.UserID)

	return result
}

// telegramSendError returns a failed send's reason, nil when it was sent:
//   - a request that never left the bot (request.ErrNotSent) is a definite
//     "not sent" with its real cause;
//   - no answer (StatusCode 0), or a success without a message id, is a
//     replyDeliveryUnknownError;
//   - a 429 or 5xx answer is a replyRetryableError;
//   - any other answer is a definite refusal with Telegram's description.
func telegramSendError(sent *message.SendMessage) error {
	var result error
	response := sent.Response
	isNotSent := errors.Is(sent.Err, request.ErrNotSent)
	isAnswered := (response.StatusCode != 0)
	isTransient := isTransientAnswer(response)
	isMissingMessage := (sent.Err == nil) && (sent.ResponsePayload.MessageId == 0)

	switch {
	case isMissingMessage == true:
		missingErr := errors.New(reportReplyNoMessageIDError)
		result = replyDeliveryUnknownError{cause: missingErr}
	case sent.Err == nil:
		result = nil
	case isNotSent == true:
		result = sent.Err
	case isAnswered == false:
		result = replyDeliveryUnknownError{cause: sent.Err}
	case isTransient == true:
		result = retryableSendError(sent)
	default:
		result = answeredSendError(sent)
	}

	return result
}

// isTransientAnswer reports an answer refused for a passing reason: 429 Too
// Many Requests or a server error.
func isTransientAnswer(response request.ResponseInfo) bool {
	isRateLimited := (response.HTTPStatus == http.StatusTooManyRequests)
	isServerError := (response.HTTPStatus >= http.StatusInternalServerError)
	result := (isRateLimited == true) || (isServerError == true)

	return result
}

// retryableSendError wraps a 429 or 5xx answer with the wait Telegram asked
// for.
func retryableSendError(sent *message.SendMessage) error {
	response := sent.Response
	cause := answeredSendError(sent)
	retryAfter := time.Duration(response.RetryAfter) * time.Second
	isRateLimited := (response.HTTPStatus == http.StatusTooManyRequests)
	result := replyRetryableError{cause: cause, retryAfter: retryAfter, isRateLimited: isRateLimited}

	return result
}

// answeredSendError is Telegram's description of a refused send, or the
// request error when the answer had none.
func answeredSendError(sent *message.SendMessage) error {
	result := sent.Err
	description := sent.Response.Message

	if description != "" {
		result = errors.New(description)
	}

	return result
}
