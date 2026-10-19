package dashboard

import (
	"context"
	"time"
)

// Store reads the dashboard data, edits the blog posts and queues report
// replies. PGStore is the
// PostgreSQL implementation; tests use a stub.
type Store interface {
	BlogStore
	ReportStore
	SettingsStore
	Ping(ctx context.Context) error
	Overview(ctx context.Context, days int) (Overview, error)
	Platforms(ctx context.Context, days int) (Platforms, error)
	Chats(ctx context.Context, days int, limit int) (Chats, error)
	Money(ctx context.Context, months int) (Money, error)
	Reports(ctx context.Context, limit int, weeks int, status string) (Reports, error)
}

// Meta is part of every response.
type Meta struct {
	GeneratedAt string `json:"generated_at"`
	Timezone    string `json:"timezone"`
}

// setMeta fills the response metadata; embedding Meta makes every response a
// payload.
func (m *Meta) setMeta(value Meta) {
	*m = value
}

// payload is a response the server stamps with Meta before writing it.
type payload interface {
	setMeta(value Meta)
}

// Health is the /healthz answer.
type Health struct {
	Meta
	OK bool `json:"ok"`
}

// errorBody is the JSON shape of every error answer.
type errorBody struct {
	Error string `json:"error"`
}

// OverviewTotals are the headline numbers of the overview.
type OverviewTotals struct {
	DownloadsAll    int64 `json:"downloads_all"`
	DownloadsRange  int64 `json:"downloads_range"`
	Downloads7d     int64 `json:"downloads_7d"`
	Downloads30d    int64 `json:"downloads_30d"`
	ActiveUsers7d   int64 `json:"active_users_7d"`
	ActiveUsers30d  int64 `json:"active_users_30d"`
	ActiveChats7d   int64 `json:"active_chats_7d"`
	ActiveChats30d  int64 `json:"active_chats_30d"`
	GroupsAll       int64 `json:"groups_all"`
	PrivateUsersAll int64 `json:"private_users_all"`
}

// DailySplit is one day's downloads in private chats and groups.
type DailySplit struct {
	Day     string `json:"day"`
	Private int64  `json:"private"`
	Group   int64  `json:"group"`
}

// PlatformCount is one platform's downloads.
type PlatformCount struct {
	Platform  string `json:"platform"`
	Downloads int64  `json:"downloads"`
}

// WeeklyNewChats counts chats whose first download fell in the week.
type WeeklyNewChats struct {
	Week    string `json:"week"`
	Groups  int64  `json:"groups"`
	Private int64  `json:"private"`
}

// Overview is the /api/overview answer.
type Overview struct {
	Meta
	Days           int              `json:"days"`
	Totals         OverviewTotals   `json:"totals"`
	Daily          []DailySplit     `json:"daily"`
	PlatformMix    []PlatformCount  `json:"platform_mix"`
	NewChatsWeekly []WeeklyNewChats `json:"new_chats_weekly"`
}

// PlatformDay is one day's downloads per platform.
type PlatformDay struct {
	Day    string           `json:"day"`
	Counts map[string]int64 `json:"counts"`
}

// MediaTypeSplit is one platform's photo and video downloads.
type MediaTypeSplit struct {
	Platform string `json:"platform"`
	Photo    int64  `json:"photo"`
	Video    int64  `json:"video"`
}

// PlatformHealth compares a platform's last 7 days with the 28 before them,
// or before its last download when it has been silent longer.
type PlatformHealth struct {
	Platform        string   `json:"platform"`
	LastDownloadAt  *string  `json:"last_download_at"`
	Downloads24h    int64    `json:"downloads_24h"`
	Downloads7d     int64    `json:"downloads_7d"`
	AvgDailyPrev28d float64  `json:"avg_daily_prev_28d"`
	AvgDaily7d      float64  `json:"avg_daily_7d"`
	ChangePct       *float64 `json:"change_pct"`
	Status          string   `json:"status"`
}

// Platforms is the /api/platforms answer.
type Platforms struct {
	Meta
	Days       int              `json:"days"`
	Platforms  []string         `json:"platforms"`
	Daily      []PlatformDay    `json:"daily"`
	MediaTypes []MediaTypeSplit `json:"media_types"`
	Health     []PlatformHealth `json:"health"`
}

// TopGroup is a group chat ranked by downloads.
type TopGroup struct {
	ChatID    int64   `json:"chat_id"`
	Title     string  `json:"title"`
	Downloads int64   `json:"downloads"`
	Users     int64   `json:"users"`
	LastAt    *string `json:"last_at"`
}

// TopUser is a private-chat user ranked by downloads.
type TopUser struct {
	UserID    int64   `json:"user_id"`
	Username  string  `json:"username"`
	Downloads int64   `json:"downloads"`
	LastAt    *string `json:"last_at"`
}

// Retention compares the chats active this calendar month with the last.
type Retention struct {
	Month           string `json:"month"`
	ActiveThisMonth int64  `json:"active_this_month"`
	ActiveLastMonth int64  `json:"active_last_month"`
	Retained        int64  `json:"retained"`
}

// ChatSetting is one chat_settings row that differs from the default.
type ChatSetting struct {
	Type    string `json:"type"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

// CustomChat is a chat with non-default settings or limits of its own.
type CustomChat struct {
	ChatID     int64         `json:"chat_id"`
	Title      string        `json:"title"`
	DailyLimit int64         `json:"daily_limit"`
	Timezone   string        `json:"timezone"`
	UserLimits int64         `json:"user_limits"`
	Settings   []ChatSetting `json:"settings"`
}

// Chats is the /api/chats answer.
type Chats struct {
	Meta
	Days           int          `json:"days"`
	Limit          int          `json:"limit"`
	TopGroups      []TopGroup   `json:"top_groups"`
	TopUsers       []TopUser    `json:"top_users"`
	Retention      Retention    `json:"retention"`
	CustomSettings []CustomChat `json:"custom_settings"`
}

// GatedMonth is one month of restricted-post usage.
type GatedMonth struct {
	Month      string `json:"month"`
	GlobalUsed int64  `json:"global_used"`
	ChatsUsed  int64  `json:"chats_used"`
	UsersUsed  int64  `json:"users_used"`
}

// GatedCurrent is a chat's or user's restricted-post usage this month.
type GatedCurrent struct {
	Scope   string `json:"scope"`
	ScopeID int64  `json:"scope_id"`
	Title   string `json:"title"`
	Used    int64  `json:"used"`
	// Limit is the custom monthly limit; null without one or when unlimited.
	Limit     *int64 `json:"limit"`
	Unlimited bool   `json:"unlimited"`
	Custom    bool   `json:"custom"`
}

// StarsMonth is one month of Stars support payments.
type StarsMonth struct {
	Month         string `json:"month"`
	Payments      int64  `json:"payments"`
	Stars         int64  `json:"stars"`
	Refunds       int64  `json:"refunds"`
	RefundedStars int64  `json:"refunded_stars"`
	Boosts        int64  `json:"boosts"`
}

// Credits are the unused bonus restricted-post credits.
type Credits struct {
	UsersWithCredits int64 `json:"users_with_credits"`
	UserCredits      int64 `json:"user_credits"`
	ChatsWithCredits int64 `json:"chats_with_credits"`
	ChatCredits      int64 `json:"chat_credits"`
}

// Money is the /api/money answer.
type Money struct {
	Meta
	Months       int            `json:"months"`
	GatedMonthly []GatedMonth   `json:"gated_monthly"`
	GatedCurrent []GatedCurrent `json:"gated_current"`
	StarsMonthly []StarsMonth   `json:"stars_monthly"`
	Credits      Credits        `json:"credits"`
}

// ReportTotals counts reports by their resolved flag.
type ReportTotals struct {
	Open     int64 `json:"open"`
	Resolved int64 `json:"resolved"`
}

// ReportWeek counts the reports created in a week.
type ReportWeek struct {
	Week     string `json:"week"`
	Open     int64  `json:"open"`
	Resolved int64  `json:"resolved"`
}

// Report is one user report with its replies, oldest first. ResolvedAt is
// null for an open report and for one resolved before resolved_at existed.
type Report struct {
	ID         int64         `json:"id"`
	CreatedAt  *string       `json:"created_at"`
	UserID     int64         `json:"user_id"`
	Username   string        `json:"username"`
	FirstName  string        `json:"first_name"`
	ChatID     int64         `json:"chat_id"`
	ChatTitle  string        `json:"chat_title"`
	Message    string        `json:"message"`
	Resolved   bool          `json:"resolved"`
	ResolvedAt *string       `json:"resolved_at"`
	Replies    []ReportReply `json:"replies"`
}

// ReportReply is one report_replies row: a reply queued here or sent with
// /reply in Telegram. Status is queued, sending, sent, failed (refused by
// Telegram or never sent) or unknown (no answer from Telegram, so it may
// have been delivered); Error is the reason, or the last passing refusal of
// a queued reply waiting for its next attempt (NextAttemptAt). ResolveOnSend
// resolves the report once the bot has delivered the reply.
type ReportReply struct {
	ID            int64   `json:"id"`
	Text          string  `json:"text"`
	Source        string  `json:"source"`
	Status        string  `json:"status"`
	ResolveOnSend bool    `json:"resolve_on_send"`
	Attempts      int64   `json:"attempts"`
	NextAttemptAt *string `json:"next_attempt_at"`
	Error         *string `json:"error"`
	CreatedAt     *string `json:"created_at"`
	SentAt        *string `json:"sent_at"`
}

// Reports is the /api/reports answer; Status is the filter of Latest.
// ReplyPrefix is the heading the bot puts above a reply and ReplyTextLimit
// the longest reply text, so the page previews and counts like the bot.
type Reports struct {
	Meta
	Limit          int          `json:"limit"`
	Weeks          int          `json:"weeks"`
	Status         string       `json:"status"`
	ReplyPrefix    string       `json:"reply_prefix"`
	ReplyTextLimit int          `json:"reply_text_limit"`
	Totals         ReportTotals `json:"totals"`
	Weekly         []ReportWeek `json:"weekly"`
	Latest         []Report     `json:"latest"`
}

// ReportStore reads one report and makes the dashboard's report writes:
// queuing a reply for the bot to send and resolving or reopening.
type ReportStore interface {
	Report(ctx context.Context, id int64) (Report, error)
	QueueReportReply(ctx context.Context, id int64, text string, resolve bool) (ReportReply, Report, error)
	SetReportResolved(ctx context.Context, id int64, resolved bool) (Report, error)
}

// ReportAnswer is the answer about one report.
type ReportAnswer struct {
	Meta
	Report Report `json:"report"`
}

// ReportReplyAnswer is the POST /api/reports/{id}/replies answer: the
// queued reply and the report after the write.
type ReportReplyAnswer struct {
	Meta
	Reply  ReportReply `json:"reply"`
	Report Report      `json:"report"`
}

// BlogStore reads and edits blog_posts, the drafts the bot's owner panel
// sends under /owner -> Broadcast. Its write methods and ReportStore's are
// the only writes of the dashboard.
type BlogStore interface {
	BlogPosts(ctx context.Context) ([]BlogPost, error)
	BlogPost(ctx context.Context, id int64) (BlogPost, error)
	CreateBlogPost(ctx context.Context, draft BlogDraft) (BlogPost, error)
	UpdateBlogPost(ctx context.Context, id int64, draft BlogDraft, loadedAt *time.Time) (BlogPost, error)
	SetBlogPostPublished(ctx context.Context, id int64, published bool) (BlogPost, error)
	DeleteBlogPost(ctx context.Context, id int64) error
}

// BlogDraft is a validated title and content.
type BlogDraft struct {
	Title   string
	Content string
}

// BlogPost is one blog_posts row; Length is the content's Telegram length.
// Timestamps are RFC3339Nano, so updated_at round-trips at the database's
// microsecond precision.
type BlogPost struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	Published bool    `json:"published"`
	CreatedAt *string `json:"created_at"`
	UpdatedAt *string `json:"updated_at"`
	Length    int     `json:"length"`
}

// BlogPosts is the GET /api/blog-posts answer.
type BlogPosts struct {
	Meta
	Posts []BlogPost `json:"posts"`
}

// BlogPostAnswer is the answer about one post.
type BlogPostAnswer struct {
	Meta
	Post BlogPost `json:"post"`
}
