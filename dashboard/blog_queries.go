package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Errors of the blog write methods; the server maps them to 404 and 409.
var (
	ErrBlogPostNotFound = errors.New("blog post not found")
	ErrBlogPostChanged  = errors.New("blog post changed elsewhere")
)

// rowScanner is a *sql.Row or *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// BlogPosts lists every post, newest first.
func (s *PGStore) BlogPosts(ctx context.Context) ([]BlogPost, error) {
	result := []BlogPost{}
	err := s.readOnly(ctx, func(q readTx) error {
		loadErr := q.rows(blogPostsSQL, func(rows *sql.Rows) error {
			post, scanErr := scanBlogPost(rows)
			result = append(result, post)

			return scanErr
		})

		return loadErr
	})

	return result, err
}

// BlogPost loads one post; ErrBlogPostNotFound when there is none.
func (s *PGStore) BlogPost(ctx context.Context, id int64) (BlogPost, error) {
	result := BlogPost{}
	err := s.readOnly(ctx, func(q readTx) error {
		var loadErr error
		result, loadErr = queryBlogPost(q, blogPostSQL, id)

		return loadErr
	})

	return result, err
}

// CreateBlogPost inserts an unpublished post.
func (s *PGStore) CreateBlogPost(ctx context.Context, draft BlogDraft) (BlogPost, error) {
	result := BlogPost{}
	err := s.readWrite(ctx, func(q writeTx) error {
		var writeErr error
		result, writeErr = queryBlogPost(q.readTx, createBlogPostSQL, draft.Title, draft.Content)

		return writeErr
	})

	return result, err
}

// UpdateBlogPost edits a post that still has the updated_at the editor
// loaded (nil for NULL); ErrBlogPostChanged when it was edited since,
// ErrBlogPostNotFound when it is gone.
func (s *PGStore) UpdateBlogPost(ctx context.Context, id int64, draft BlogDraft, loadedAt *time.Time) (BlogPost, error) {
	result := BlogPost{}
	loaded := nullTime(loadedAt)
	err := s.readWrite(ctx, func(q writeTx) error {
		var writeErr error
		result, writeErr = queryBlogPost(q.readTx, updateBlogPostSQL, id, draft.Title, draft.Content, loaded)

		if errors.Is(writeErr, ErrBlogPostNotFound) == true {
			writeErr = missingOrChanged(q, id)
		}

		return writeErr
	})

	return result, err
}

// nullTime turns an optional timestamp into a bind parameter.
func nullTime(value *time.Time) sql.NullTime {
	result := sql.NullTime{}

	if value != nil {
		result = sql.NullTime{Time: *value, Valid: true}
	}

	return result
}

// missingOrChanged tells why a conditional update matched no row.
func missingOrChanged(q writeTx, id int64) error {
	exists := false
	result := q.row(blogPostExistsSQL, []interface{}{&exists}, id)

	if (result == nil) && (exists == true) {
		result = ErrBlogPostChanged
	}

	if (result == nil) && (exists == false) {
		result = ErrBlogPostNotFound
	}

	return result
}

// SetBlogPostPublished sets the published flag only, so a concurrent edit
// of the text in Telegram is kept.
func (s *PGStore) SetBlogPostPublished(ctx context.Context, id int64, published bool) (BlogPost, error) {
	result := BlogPost{}
	err := s.readWrite(ctx, func(q writeTx) error {
		var writeErr error
		result, writeErr = queryBlogPost(q.readTx, setBlogPostPublishedSQL, id, published)

		return writeErr
	})

	return result, err
}

// DeleteBlogPost removes a post; ErrBlogPostNotFound when there is none.
func (s *PGStore) DeleteBlogPost(ctx context.Context, id int64) error {
	err := s.readWrite(ctx, func(q writeTx) error {
		var deletedID int64
		writeErr := q.row(deleteBlogPostSQL, []interface{}{&deletedID}, id)

		if errors.Is(writeErr, sql.ErrNoRows) == true {
			writeErr = ErrBlogPostNotFound
		}

		return writeErr
	})

	return err
}

// queryBlogPost runs a query returning one post; no row is
// ErrBlogPostNotFound.
func queryBlogPost(q readTx, query string, args ...interface{}) (BlogPost, error) {
	row := q.tx.QueryRowContext(q.ctx, query, args...)
	result, err := scanBlogPost(row)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = ErrBlogPostNotFound
	}

	return result, err
}

// scanBlogPost reads the blogPostColumns and computes the Telegram length.
func scanBlogPost(row rowScanner) (BlogPost, error) {
	result := BlogPost{}
	var createdAt sql.NullTime
	var updatedAt sql.NullTime
	err := row.Scan(&result.ID, &result.Title, &result.Content, &result.Published, &createdAt, &updatedAt)

	if err == nil {
		result.CreatedAt = formatNullPreciseTime(createdAt)
		result.UpdatedAt = formatNullPreciseTime(updatedAt)
		result.Length = telegramLength(result.Content)
	}

	return result, err
}

// formatNullPreciseTime renders a nullable timestamp as RFC3339Nano in UTC.
func formatNullPreciseTime(value sql.NullTime) *string {
	var result *string

	if value.Valid == true {
		utc := value.Time.UTC()
		formatted := utc.Format(time.RFC3339Nano)
		result = &formatted
	}

	return result
}
