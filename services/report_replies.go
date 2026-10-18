package services

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/timoffmax/social-link-bot/model/reportreply"
)

// Sources of a report reply.
const (
	ReportReplySourceDashboard = reportreply.SourceDashboard
	ReportReplySourceTelegram  = reportreply.SourceTelegram
)

// Statuses of a report reply (see reportreply). A 'sending' row was claimed
// by a sender and is never sent again automatically, so a crash can not send
// a reply twice.
const (
	ReportReplyStatusQueued  = reportreply.StatusQueued
	ReportReplyStatusSending = reportreply.StatusSending
	ReportReplyStatusSent    = reportreply.StatusSent
	ReportReplyStatusFailed  = reportreply.StatusFailed
	ReportReplyStatusUnknown = reportreply.StatusUnknown
)

// ReportReplyPrefix opens every reply sent to the reporter (HTML parse mode).
const ReportReplyPrefix = reportreply.Prefix

// ReportReplyInterruptedError is the error of a reply whose send was cut off
// (it stayed 'sending' longer than ReportReplyStuckAfter): it may have been
// delivered and is not resent.
const ReportReplyInterruptedError = "interrupted, delivery unknown, not resent"

// ReportReplyStuckAfter is how long a reply may stay 'sending' after its
// claim before it counts as interrupted. It stays well above the Telegram
// client timeout (request.ClientTimeout), so a send still waiting for its
// answer is never taken for an interrupted one.
const ReportReplyStuckAfter = 5 * time.Minute

// errDatabaseNotInitialized is returned by the store when there is no database.
var errDatabaseNotInitialized = errors.New("database not initialized")

// reportReplyColumns are the columns every reply query returns, in the order
// scanReportReply reads them.
const reportReplyColumns = "id, report_id, text, source, status, resolve_on_send, attempts, next_attempt_at, claimed_at, error, telegram_message_id, created_at, sent_at"

// ReportReply is a reply to a user report.
type ReportReply struct {
	ID                int
	ReportID          int
	Text              string
	Source            string
	Status            string
	ResolveOnSend     bool
	Attempts          int
	NextAttemptAt     sql.NullTime
	ClaimedAt         sql.NullTime
	Error             sql.NullString
	TelegramMessageID sql.NullInt64
	CreatedAt         time.Time
	SentAt            sql.NullTime
}

// ClaimedReportReply is a reply claimed for sending, with the reporter it goes to.
type ClaimedReportReply struct {
	Reply  ReportReply
	UserID int64
}

// rowScanner is a *sql.Row or *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanReportReply reads one row of reportReplyColumns.
func scanReportReply(row rowScanner, extra ...interface{}) (ReportReply, error) {
	var result ReportReply
	destinations := []interface{}{
		&result.ID,
		&result.ReportID,
		&result.Text,
		&result.Source,
		&result.Status,
		&result.ResolveOnSend,
		&result.Attempts,
		&result.NextAttemptAt,
		&result.ClaimedAt,
		&result.Error,
		&result.TelegramMessageID,
		&result.CreatedAt,
		&result.SentAt,
	}
	destinations = append(destinations, extra...)
	err := row.Scan(destinations...)

	return result, err
}

// createReportReplyQuery inserts a reply; a reply created as 'sending' is
// claimed at once: $1 report id, $2 text, $3 source, $4 status, $5 'sending'.
const createReportReplyQuery = `
INSERT INTO report_replies (report_id, text, source, status, claimed_at)
VALUES ($1, $2::text, $3::text, $4::text, CASE WHEN $4::text = $5::text THEN now() END)
RETURNING ` + reportReplyColumns

// CreateReportReply records a reply to a report with the given source and
// status ('queued' for the outbox, 'sending' when the caller sends it now).
func CreateReportReply(reportID int, text, source, status string) (ReportReply, error) {
	var result ReportReply
	err := errDatabaseNotInitialized

	if IsDBInitialized() == true {
		row := GetDB().QueryRow(createReportReplyQuery, reportID, text, source, status, ReportReplyStatusSending)
		result, err = scanReportReply(row)
	}

	return result, err
}

// claimReportReplyQuery moves the oldest queued reply that is due to
// 'sending', stamps claimed_at and returns it with the reporter's user id;
// SKIP LOCKED keeps two senders apart: $1 'sending', $2 'queued'.
const claimReportReplyQuery = `
WITH claimed AS (
    UPDATE report_replies SET status = $1, claimed_at = now()
    WHERE id = (
        SELECT id FROM report_replies
        WHERE status = $2 AND (next_attempt_at IS NULL OR next_attempt_at <= now())
        ORDER BY created_at, id
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    )
    RETURNING ` + reportReplyColumns + `
)
SELECT claimed.*, reports.user_id
FROM claimed
JOIN reports ON reports.id = claimed.report_id`

// ClaimNextQueuedReportReply claims the oldest queued reply that is due for
// sending. The bool is false when nothing is due.
func ClaimNextQueuedReportReply() (ClaimedReportReply, bool, error) {
	var result ClaimedReportReply
	isClaimed := false
	err := errDatabaseNotInitialized

	if IsDBInitialized() == true {
		row := GetDB().QueryRow(claimReportReplyQuery, ReportReplyStatusSending, ReportReplyStatusQueued)
		result.Reply, err = scanReportReply(row, &result.UserID)
		isClaimed = (err == nil)
	}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, isClaimed, err
}

// markReportReplySentQuery records the delivered message and, for a reply
// queued with resolve_on_send, resolves its report (keeping an earlier
// resolved_at) in the same statement, so both or neither are stored:
// $1 id, $2 message id, $3 'sent'.
const markReportReplySentQuery = `
WITH sent AS (
    UPDATE report_replies
    SET status = $3, error = NULL, telegram_message_id = $2, sent_at = now()
    WHERE id = $1
    RETURNING report_id, resolve_on_send
)
UPDATE reports
SET resolved = true, resolved_at = COALESCE(reports.resolved_at, now())
FROM sent
WHERE reports.id = sent.report_id AND sent.resolve_on_send`

// MarkReportReplySent records the delivered message and resolves the report
// when the reply asked for it. It is not limited to 'sending' rows: when a
// reply did reach the user, 'sent' is the truth.
func MarkReportReplySent(id int, telegramMessageID int64) error {
	err := errDatabaseNotInitialized

	if IsDBInitialized() == true {
		_, err = GetDB().Exec(markReportReplySentQuery, id, telegramMessageID, ReportReplyStatusSent)
	}

	return err
}

// markReportReplyOutcomeQuery records why a claimed reply was not delivered;
// a reply no longer 'sending' (e.g. already sent) is left alone: $1 id,
// $2 status, $3 reason, $4 'sending'.
const markReportReplyOutcomeQuery = `UPDATE report_replies SET status = $2, error = $3 WHERE id = $1 AND status = $4`

// MarkReportReplyFailed records a definite refusal (or a reply that never
// left the bot) with its reason.
func MarkReportReplyFailed(id int, reason string) error {
	result := markReportReplyOutcome(id, ReportReplyStatusFailed, reason)

	return result
}

// MarkReportReplyUnknown records a send without an answer from Telegram: it
// may have been delivered.
func MarkReportReplyUnknown(id int, reason string) error {
	result := markReportReplyOutcome(id, ReportReplyStatusUnknown, reason)

	return result
}

// markReportReplyOutcome moves a 'sending' reply to status with reason.
func markReportReplyOutcome(id int, status string, reason string) error {
	err := errDatabaseNotInitialized

	if IsDBInitialized() == true {
		_, err = GetDB().Exec(markReportReplyOutcomeQuery, id, status, reason, ReportReplyStatusSending)
	}

	return err
}

// requeueReportReplyQuery puts a claimed reply back in the queue after a
// transient refusal: $1 id, $2 reason, $3 not before, $4 'queued',
// $5 'sending'.
const requeueReportReplyQuery = `
UPDATE report_replies
SET status = $4, attempts = attempts + 1, next_attempt_at = $3, claimed_at = NULL, error = $2
WHERE id = $1 AND status = $5`

// RequeueReportReply puts a 'sending' reply back to 'queued' after a
// transient refusal (429, 5xx), counting the attempt; it is not claimed
// before notBefore.
func RequeueReportReply(id int, reason string, notBefore time.Time) error {
	err := errDatabaseNotInitialized

	if IsDBInitialized() == true {
		_, err = GetDB().Exec(requeueReportReplyQuery, id, reason, notBefore, ReportReplyStatusQueued, ReportReplyStatusSending)
	}

	return err
}

// markInterruptedReportRepliesQuery moves replies claimed longer ago than the
// stuck window to 'unknown': $1 'unknown', $2 reason, $3 'sending',
// $4 window in seconds.
const markInterruptedReportRepliesQuery = `
UPDATE report_replies SET status = $1, error = $2
WHERE status = $3 AND COALESCE(claimed_at, created_at) < now() - make_interval(secs => $4)
RETURNING ` + reportReplyColumns

// MarkInterruptedReportReplies marks replies that have been 'sending' for
// longer than ReportReplyStuckAfter since their claim as 'unknown', so they
// are never resent, and returns them.
func MarkInterruptedReportReplies() ([]ReportReply, error) {
	var result []ReportReply
	var rows *sql.Rows
	err := errDatabaseNotInitialized
	stuckSeconds := ReportReplyStuckAfter.Seconds()

	if IsDBInitialized() == true {
		rows, err = GetDB().Query(markInterruptedReportRepliesQuery, ReportReplyStatusUnknown, ReportReplyInterruptedError, ReportReplyStatusSending, stuckSeconds)
	}

	if err == nil {
		defer rows.Close()
		result, err = scanReportReplies(rows)
	}

	return result, err
}

// ListReportReplies returns a report's replies, oldest first.
func ListReportReplies(reportID int) ([]ReportReply, error) {
	var result []ReportReply
	var rows *sql.Rows
	err := errDatabaseNotInitialized

	if IsDBInitialized() == true {
		query := fmt.Sprintf("SELECT %s FROM report_replies WHERE report_id = $1 ORDER BY created_at, id", reportReplyColumns)
		rows, err = GetDB().Query(query, reportID)
	}

	if err == nil {
		defer rows.Close()
		result, err = scanReportReplies(rows)
	}

	return result, err
}

// scanReportReplies reads every row of reportReplyColumns.
func scanReportReplies(rows *sql.Rows) ([]ReportReply, error) {
	var result []ReportReply
	var err error

	for (err == nil) && (rows.Next() == true) {
		var reply ReportReply
		reply, err = scanReportReply(rows)

		if err == nil {
			result = append(result, reply)
		}
	}

	if err == nil {
		err = rows.Err()
	}

	return result, err
}
