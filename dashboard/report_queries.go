package dashboard

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"github.com/timoffmax/social-link-bot/model/reportreply"
)

// ErrReportNotFound is the error of a report method for a missing report;
// the server maps it to 404.
var ErrReportNotFound = errors.New("report not found")

// Report loads one report with its replies; ErrReportNotFound when there is
// none.
func (s *PGStore) Report(ctx context.Context, id int64) (Report, error) {
	result := Report{}
	err := s.readOnly(ctx, func(q readTx) error {
		var loadErr error
		result, loadErr = queryReport(q, reportSQL, id)

		return loadErr
	})

	return result, err
}

// QueueReportReply queues a reply for the bot to send to the report's user
// and returns it with the report. The dashboard never resolves here: with
// resolve the reply carries resolve_on_send, and the bot resolves the report
// in the same transaction that marks the reply sent, so a reply that fails
// or whose delivery is unknown leaves the report open.
func (s *PGStore) QueueReportReply(ctx context.Context, id int64, text string, resolve bool) (ReportReply, Report, error) {
	reply := ReportReply{}
	report := Report{}
	err := s.readWrite(ctx, func(q writeTx) error {
		var writeErr error
		reply, writeErr = insertReportReply(q, id, text, resolve)

		if writeErr == nil {
			report, writeErr = queryReport(q.readTx, reportSQL, id)
		}

		return writeErr
	})

	return reply, report, err
}

// insertReportReply inserts a queued dashboard reply; ErrReportNotFound
// when the report does not exist.
func insertReportReply(q writeTx, id int64, text string, resolveOnSend bool) (ReportReply, error) {
	row := q.tx.QueryRowContext(q.ctx, queueReportReplySQL, id, text, reportreply.SourceDashboard, reportreply.StatusQueued, resolveOnSend)
	var reportID int64
	result, err := scanReportReply(row, &reportID)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = ErrReportNotFound
	}

	return result, err
}

// SetReportResolved resolves (resolved_at now, or the earlier one when it
// already is resolved) or reopens (resolved_at NULL) a report.
func (s *PGStore) SetReportResolved(ctx context.Context, id int64, resolved bool) (Report, error) {
	result := Report{}
	err := s.readWrite(ctx, func(q writeTx) error {
		var writeErr error
		result, writeErr = queryReport(q.readTx, setReportResolvedSQL, id, resolved)

		return writeErr
	})

	return result, err
}

// queryReport runs a query returning one report and loads its replies; no
// row is ErrReportNotFound.
func queryReport(q readTx, query string, args ...interface{}) (Report, error) {
	row := q.tx.QueryRowContext(q.ctx, query, args...)
	result, err := scanReport(row)
	reports := []Report{result}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = ErrReportNotFound
	}

	if err == nil {
		err = attachReportReplies(q, reports)
	}

	if err == nil {
		result = reports[0]
	}

	return result, err
}

// reportStatusFilter turns the status filter into the resolved flag to
// keep: NULL keeps every report.
func reportStatusFilter(status string) sql.NullBool {
	result := sql.NullBool{}

	switch status {
	case ReportStatusOpen:
		result = sql.NullBool{Bool: false, Valid: true}
	case ReportStatusResolved:
		result = sql.NullBool{Bool: true, Valid: true}
	}

	return result
}

// scanReport reads the reportColumns; Replies starts empty.
func scanReport(row rowScanner) (Report, error) {
	result := Report{Replies: []ReportReply{}}
	var createdAt sql.NullTime
	var resolvedAt sql.NullTime
	err := row.Scan(
		&result.ID,
		&createdAt,
		&result.UserID,
		&result.Username,
		&result.FirstName,
		&result.ChatID,
		&result.ChatTitle,
		&result.Message,
		&result.Resolved,
		&resolvedAt,
	)

	if err == nil {
		result.CreatedAt = formatNullTime(createdAt)
		result.ResolvedAt = formatNullTime(resolvedAt)
	}

	return result, err
}

// scanReportReply reads report_id into reportID and the replyColumns.
func scanReportReply(row rowScanner, reportID *int64) (ReportReply, error) {
	result := ReportReply{}
	var replyError sql.NullString
	var nextAttemptAt sql.NullTime
	var createdAt sql.NullTime
	var sentAt sql.NullTime
	err := row.Scan(
		reportID,
		&result.ID,
		&result.Text,
		&result.Source,
		&result.Status,
		&result.ResolveOnSend,
		&result.Attempts,
		&nextAttemptAt,
		&replyError,
		&createdAt,
		&sentAt,
	)

	if err == nil {
		result.Error = nullString(replyError)
		result.NextAttemptAt = formatNullTime(nextAttemptAt)
		result.CreatedAt = formatNullTime(createdAt)
		result.SentAt = formatNullTime(sentAt)
	}

	return result, err
}

// nullString turns a nullable text column into a JSON string or null.
func nullString(value sql.NullString) *string {
	var result *string

	if value.Valid == true {
		text := value.String
		result = &text
	}

	return result
}

// attachReportReplies loads the replies of the reports, oldest first, into
// their Replies.
func attachReportReplies(q readTx, reports []Report) error {
	var err error
	ids := make([]int64, 0, len(reports))
	indexByReport := map[int64]int{}

	for index, report := range reports {
		ids = append(ids, report.ID)
		indexByReport[report.ID] = index
	}

	idArray := pq.Array(ids)

	if len(ids) > 0 {
		err = q.rows(reportRepliesSQL, func(rows *sql.Rows) error {
			var reportID int64
			reply, scanErr := scanReportReply(rows, &reportID)
			index, isKnown := indexByReport[reportID]

			if (scanErr == nil) && (isKnown == true) {
				reports[index].Replies = append(reports[index].Replies, reply)
			}

			return scanErr
		}, idArray)
	}

	return err
}
