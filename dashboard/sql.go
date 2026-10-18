package dashboard

// SQL used by PGStore. Every value comes in as a bind parameter; the
// comment above each query names them.

// setStatementTimeoutSQL limits every statement of the current transaction
// ($1 = timeout, e.g. "5s"); it is SET LOCAL with a bind parameter.
const setStatementTimeoutSQL = `SELECT set_config('statement_timeout', $1::text, true)`

// rangeCTE is the day range ending today in the dashboard timezone:
// $1 timezone, $2 now, $3 number of days.
const rangeCTE = `bounds AS (
    SELECT
        today.local_day - ($3::int - 1) AS start_day,
        today.local_day AS end_day,
        ((today.local_day - ($3::int - 1))::timestamp AT TIME ZONE $1::text) AS start_at,
        ((today.local_day + 1)::timestamp AT TIME ZONE $1::text) AS end_at
    FROM (SELECT ($2::timestamptz AT TIME ZONE $1::text)::date AS local_day) AS today
)`

// rangeDaysCTE lists every day of the range (needs rangeCTE).
const rangeDaysCTE = `days AS (
    SELECT gs::date AS day
    FROM bounds b, generate_series(b.start_day::timestamp, b.end_day::timestamp, INTERVAL '1 day') AS gs
)`

// overviewTotalsSQL: $1 timezone, $2 now, $3 days, $4 short window days,
// $5 long window days.
const overviewTotalsSQL = `WITH ` + rangeCTE + `
SELECT
    COUNT(*),
    COUNT(*) FILTER (WHERE m.created_at >= b.start_at AND m.created_at < b.end_at),
    COUNT(*) FILTER (WHERE m.created_at > $2::timestamptz - make_interval(days => $4::int)),
    COUNT(*) FILTER (WHERE m.created_at > $2::timestamptz - make_interval(days => $5::int)),
    COUNT(DISTINCT m.user_id) FILTER (WHERE m.created_at > $2::timestamptz - make_interval(days => $4::int)),
    COUNT(DISTINCT m.user_id) FILTER (WHERE m.created_at > $2::timestamptz - make_interval(days => $5::int)),
    COUNT(DISTINCT m.chat_id) FILTER (WHERE m.created_at > $2::timestamptz - make_interval(days => $4::int)),
    COUNT(DISTINCT m.chat_id) FILTER (WHERE m.created_at > $2::timestamptz - make_interval(days => $5::int)),
    COUNT(DISTINCT m.chat_id) FILTER (WHERE m.chat_id < 0),
    COUNT(DISTINCT m.chat_id) FILTER (WHERE m.chat_id > 0)
FROM media_stats m
CROSS JOIN bounds b`

// overviewDailySQL: $1 timezone, $2 now, $3 days.
const overviewDailySQL = `WITH ` + rangeCTE + `,
` + rangeDaysCTE + `,
counts AS (
    SELECT
        (m.created_at AT TIME ZONE $1::text)::date AS day,
        COUNT(*) FILTER (WHERE m.chat_id > 0) AS private_count,
        COUNT(*) FILTER (WHERE m.chat_id < 0) AS group_count
    FROM media_stats m
    CROSS JOIN bounds b
    WHERE m.created_at >= b.start_at AND m.created_at < b.end_at
    GROUP BY 1
)
SELECT to_char(d.day, 'YYYY-MM-DD'), COALESCE(c.private_count, 0), COALESCE(c.group_count, 0)
FROM days d
LEFT JOIN counts c ON c.day = d.day
ORDER BY d.day`

// platformMixSQL: $1 timezone, $2 now, $3 days.
const platformMixSQL = `WITH ` + rangeCTE + `
SELECT m.platform, COUNT(*) AS downloads
FROM media_stats m
CROSS JOIN bounds b
WHERE m.created_at >= b.start_at AND m.created_at < b.end_at
GROUP BY m.platform
ORDER BY downloads DESC, m.platform`

// newChatsWeeklySQL: $1 timezone, $2 now, $3 days. A chat counts in the
// (Monday) week of its first download ever; one aggregate pass keeps only
// the chats first seen since the first week of the range.
const newChatsWeeklySQL = `WITH ` + rangeCTE + `,
weeks AS (
    SELECT gs::date AS week
    FROM bounds b, generate_series(
        date_trunc('week', b.start_day::timestamp),
        date_trunc('week', b.end_day::timestamp),
        INTERVAL '1 week'
    ) AS gs
),
range_start AS (
    SELECT (date_trunc('week', b.start_day::timestamp) AT TIME ZONE $1::text) AS first_week_at
    FROM bounds b
),
firsts AS (
    SELECT chat_id, date_trunc('week', MIN(created_at) AT TIME ZONE $1::text)::date AS week
    FROM media_stats
    GROUP BY chat_id
    HAVING MIN(created_at) >= (SELECT first_week_at FROM range_start)
),
counts AS (
    SELECT
        week,
        COUNT(*) FILTER (WHERE chat_id < 0) AS group_count,
        COUNT(*) FILTER (WHERE chat_id > 0) AS private_count
    FROM firsts
    GROUP BY week
)
SELECT to_char(w.week, 'YYYY-MM-DD'), COALESCE(c.group_count, 0), COALESCE(c.private_count, 0)
FROM weeks w
LEFT JOIN counts c ON c.week = w.week
ORDER BY w.week`

// platformDailySQL: $1 timezone, $2 now, $3 days. Every day comes back;
// platform is NULL on a day of a range without any download.
const platformDailySQL = `WITH ` + rangeCTE + `,
` + rangeDaysCTE + `,
platforms AS (
    SELECT DISTINCT m.platform
    FROM media_stats m
    CROSS JOIN bounds b
    WHERE m.created_at >= b.start_at AND m.created_at < b.end_at
),
counts AS (
    SELECT (m.created_at AT TIME ZONE $1::text)::date AS day, m.platform, COUNT(*) AS downloads
    FROM media_stats m
    CROSS JOIN bounds b
    WHERE m.created_at >= b.start_at AND m.created_at < b.end_at
    GROUP BY 1, 2
)
SELECT to_char(d.day, 'YYYY-MM-DD'), p.platform, COALESCE(c.downloads, 0)
FROM days d
LEFT JOIN platforms p ON true
LEFT JOIN counts c ON c.day = d.day AND c.platform = p.platform
ORDER BY d.day, p.platform`

// mediaTypesSQL: $1 timezone, $2 now, $3 days, $4 photo type, $5 video type.
const mediaTypesSQL = `WITH ` + rangeCTE + `
SELECT
    m.platform,
    COUNT(*) FILTER (WHERE m.media_type = $4::text),
    COUNT(*) FILTER (WHERE m.media_type = $5::text)
FROM media_stats m
CROSS JOIN bounds b
WHERE m.created_at >= b.start_at AND m.created_at < b.end_at
GROUP BY m.platform
ORDER BY COUNT(*) DESC, m.platform`

// platformHealthSQL: $1 now, $2 recent window days, $3 baseline window
// days, $4 the short "24h" window in hours, $5 days a platform stays listed
// after its last download. The baseline is the window before the recent
// one, or before the last download when that is earlier, so a dead platform
// keeps the baseline it had.
const platformHealthSQL = `WITH seen AS (
    SELECT
        platform,
        MAX(created_at) AS last_at,
        COUNT(*) FILTER (WHERE created_at > $1::timestamptz - make_interval(hours => $4::int)) AS last_day,
        COUNT(*) FILTER (WHERE created_at > $1::timestamptz - make_interval(days => $2::int)) AS recent
    FROM media_stats
    WHERE created_at > $1::timestamptz - make_interval(days => $5::int)
    GROUP BY platform
),
baselines AS (
    SELECT
        platform,
        last_at,
        last_day,
        recent,
        LEAST(last_at, $1::timestamptz - make_interval(days => $2::int)) AS baseline_end
    FROM seen
)
SELECT b.platform, b.last_at, b.last_day, b.recent, COUNT(m.id) AS previous
FROM baselines b
LEFT JOIN media_stats m ON m.platform = b.platform
    AND m.created_at > b.baseline_end - make_interval(days => $3::int)
    AND m.created_at <= b.baseline_end
GROUP BY b.platform, b.last_at, b.last_day, b.recent
ORDER BY b.recent + COUNT(m.id) DESC, b.platform`

// chatTitlesCTE finds the latest non-empty chat title in media_stats for
// the chats of a title_chats CTE the query defines before it. media_stats
// has no index on chat_id alone, so one filtered pass over it is cheaper
// than a lookup per chat when many chats are selected; only the rows of
// those chats are sorted.
const chatTitlesCTE = `chat_titles AS (
    SELECT DISTINCT ON (ms.chat_id) ms.chat_id, ms.chat_title
    FROM media_stats ms
    WHERE ms.chat_id IN (SELECT chat_id FROM title_chats) AND COALESCE(ms.chat_title, '') <> ''
    ORDER BY ms.chat_id, ms.created_at DESC NULLS LAST, ms.id DESC
)`

// topGroupsSQL: $1 timezone, $2 now, $3 days, $4 limit. Names come from a
// LATERAL lookup per selected chat or user (here and in topUsersSQL and
// gatedCurrentSQL): the latest non-empty chat title or username in
// media_stats. The few top groups and the user_id index keep those cheap.
const topGroupsSQL = `WITH ` + rangeCTE + `,
totals AS (
    SELECT m.chat_id, COUNT(*) AS downloads, COUNT(DISTINCT m.user_id) AS users, MAX(m.created_at) AS last_at
    FROM media_stats m
    CROSS JOIN bounds b
    WHERE m.chat_id < 0 AND m.created_at >= b.start_at AND m.created_at < b.end_at
    GROUP BY m.chat_id
    ORDER BY downloads DESC, m.chat_id
    LIMIT $4::int
)
SELECT t.chat_id, COALESCE(lt.chat_title, NULLIF(g.chat_title, ''), ''), t.downloads, t.users, t.last_at
FROM totals t
LEFT JOIN LATERAL (
    SELECT ms.chat_title
    FROM media_stats ms
    WHERE ms.chat_id = t.chat_id AND COALESCE(ms.chat_title, '') <> ''
    ORDER BY ms.created_at DESC NULLS LAST, ms.id DESC
    LIMIT 1
) lt ON true
LEFT JOIN group_media_limits g ON g.chat_id = t.chat_id
ORDER BY t.downloads DESC, t.chat_id`

// topUsersSQL: $1 timezone, $2 now, $3 days, $4 limit. Private chats only.
const topUsersSQL = `WITH ` + rangeCTE + `,
totals AS (
    SELECT m.user_id, COUNT(*) AS downloads, MAX(m.created_at) AS last_at
    FROM media_stats m
    CROSS JOIN bounds b
    WHERE m.chat_id > 0 AND m.created_at >= b.start_at AND m.created_at < b.end_at
    GROUP BY m.user_id
    ORDER BY downloads DESC, m.user_id
    LIMIT $4::int
)
SELECT t.user_id, COALESCE(lu.username, ''), t.downloads, t.last_at
FROM totals t
LEFT JOIN LATERAL (
    SELECT ms.username
    FROM media_stats ms
    WHERE ms.user_id = t.user_id AND COALESCE(ms.username, '') <> ''
    ORDER BY ms.created_at DESC NULLS LAST, ms.id DESC
    LIMIT 1
) lu ON true
ORDER BY t.downloads DESC, t.user_id`

// retentionSQL: $1 timezone, $2 now. Calendar months in the timezone.
const retentionSQL = `WITH months AS (
    SELECT date_trunc('month', $2::timestamptz AT TIME ZONE $1::text) AS this_start
),
ranges AS (
    SELECT
        this_start,
        ((this_start - INTERVAL '1 month') AT TIME ZONE $1::text) AS prev_at,
        (this_start AT TIME ZONE $1::text) AS this_at,
        ((this_start + INTERVAL '1 month') AT TIME ZONE $1::text) AS next_at
    FROM months
),
this_chats AS (
    SELECT DISTINCT m.chat_id
    FROM media_stats m
    CROSS JOIN ranges r
    WHERE m.created_at >= r.this_at AND m.created_at < r.next_at
),
prev_chats AS (
    SELECT DISTINCT m.chat_id
    FROM media_stats m
    CROSS JOIN ranges r
    WHERE m.created_at >= r.prev_at AND m.created_at < r.this_at
)
SELECT
    to_char(r.this_start, 'YYYY-MM'),
    (SELECT COUNT(*) FROM this_chats),
    (SELECT COUNT(*) FROM prev_chats),
    (SELECT COUNT(*) FROM this_chats t JOIN prev_chats p ON p.chat_id = t.chat_id)
FROM ranges r`

// customSettingsCTE keeps the chat settings that differ from the defaults
// the bot reads ($1 platform setting type, $2 the false spellings of
// strconv.ParseBool). Every setting defaults to on: a platform is off by its
// enabled flag, any other setting by a false setting_value. The defaults the
// migrations and InitializeChatSettings insert are left out.
const customSettingsCTE = `custom_settings AS (
    SELECT chat_id, setting_type, setting_key, setting_value, enabled
    FROM chat_settings
    WHERE (setting_type = $1::text AND enabled = false)
        OR (setting_type <> $1::text AND setting_value = ANY($2::text[]))
)`

// customChatsSQL: $1 platform setting type, $2 false setting values, $3
// default group timezone (the column default).
const customChatsSQL = `WITH ` + customSettingsCTE + `,
custom_chats AS (
    SELECT chat_id FROM custom_settings
    UNION
    SELECT chat_id FROM group_media_limits WHERE default_daily_limit > 0
    UNION
    SELECT chat_id FROM user_media_limits
),
title_chats AS (
    SELECT chat_id FROM custom_chats
),
` + chatTitlesCTE + `,
user_limit_counts AS (
    SELECT chat_id, COUNT(*) AS user_limits
    FROM user_media_limits
    GROUP BY chat_id
)
SELECT
    c.chat_id,
    COALESCE(lt.chat_title, NULLIF(g.chat_title, ''), ''),
    COALESCE(g.default_daily_limit, 0),
    COALESCE(g.timezone, $3::text),
    COALESCE(u.user_limits, 0)
FROM custom_chats c
LEFT JOIN chat_titles lt ON lt.chat_id = c.chat_id
LEFT JOIN group_media_limits g ON g.chat_id = c.chat_id
LEFT JOIN user_limit_counts u ON u.chat_id = c.chat_id
ORDER BY c.chat_id`

// chatSettingsSQL lists the non-default chat settings, sorted for grouping
// by chat: $1 platform setting type, $2 false setting values.
const chatSettingsSQL = `WITH ` + customSettingsCTE + `
SELECT chat_id, setting_type, setting_key, setting_value, enabled
FROM custom_settings
ORDER BY chat_id, setting_type, setting_key`

// gatedMonthlySQL: $1 now, $2 period timezone (UTC), $3 months, $4 global
// scope, $5 chat scope, $6 user scope. Usage periods are UTC month starts.
const gatedMonthlySQL = `WITH months AS (
    SELECT gs::date AS period
    FROM generate_series(
        date_trunc('month', $1::timestamptz AT TIME ZONE $2::text) - make_interval(months => $3::int - 1),
        date_trunc('month', $1::timestamptz AT TIME ZONE $2::text),
        INTERVAL '1 month'
    ) AS gs
)
SELECT
    to_char(m.period, 'YYYY-MM'),
    COALESCE(SUM(u.used) FILTER (WHERE u.scope = $4::text), 0),
    COALESCE(SUM(u.used) FILTER (WHERE u.scope = $5::text), 0),
    COALESCE(SUM(u.used) FILTER (WHERE u.scope = $6::text), 0)
FROM months m
LEFT JOIN gated_fetch_usage u ON u.period = m.period
GROUP BY m.period
ORDER BY m.period`

// gatedCurrentSQL: $1 now, $2 period timezone (UTC), $3 chat scope, $4 user
// scope. Only a policy row with a monthly_limit is a custom limit; a NULL
// monthly_limit means the bot's default limit applies.
const gatedCurrentSQL = `WITH current_usage AS (
    SELECT scope, scope_id, used
    FROM gated_fetch_usage
    WHERE period = date_trunc('month', $1::timestamptz AT TIME ZONE $2::text)::date
        AND scope IN ($3::text, $4::text)
),
title_chats AS (
    SELECT scope_id AS chat_id FROM current_usage WHERE scope = $3::text
),
` + chatTitlesCTE + `
SELECT
    u.scope,
    u.scope_id,
    CASE
        WHEN u.scope = $3::text THEN COALESCE(lt.chat_title, NULLIF(g.chat_title, ''), '')
        ELSE COALESCE(lu.username, '')
    END,
    u.used,
    p.monthly_limit,
    (p.monthly_limit IS NOT NULL)
FROM current_usage u
LEFT JOIN chat_titles lt ON u.scope = $3::text AND lt.chat_id = u.scope_id
LEFT JOIN group_media_limits g ON u.scope = $3::text AND g.chat_id = u.scope_id
LEFT JOIN LATERAL (
    SELECT ms.username
    FROM media_stats ms
    WHERE u.scope = $4::text AND ms.user_id = u.scope_id AND COALESCE(ms.username, '') <> ''
    ORDER BY ms.created_at DESC NULLS LAST, ms.id DESC
    LIMIT 1
) lu ON true
LEFT JOIN gated_fetch_policies p ON p.scope = u.scope AND p.scope_id = u.scope_id
ORDER BY u.used DESC, u.scope, u.scope_id`

// starsMonthlySQL: $1 timezone, $2 now, $3 months. Payments and boosts
// exclude refunded ones; refunds count in the month of the refund.
const starsMonthlySQL = `WITH months AS (
    SELECT gs::date AS month
    FROM generate_series(
        date_trunc('month', $2::timestamptz AT TIME ZONE $1::text) - make_interval(months => $3::int - 1),
        date_trunc('month', $2::timestamptz AT TIME ZONE $1::text),
        INTERVAL '1 month'
    ) AS gs
),
paid AS (
    SELECT
        date_trunc('month', created_at AT TIME ZONE $1::text)::date AS month,
        COUNT(*) AS payments,
        SUM(stars) AS stars,
        COUNT(*) FILTER (WHERE boosted_chat_id IS NOT NULL) AS boosts
    FROM support_payments
    WHERE refunded_at IS NULL
    GROUP BY 1
),
refunded AS (
    SELECT
        date_trunc('month', refunded_at AT TIME ZONE $1::text)::date AS month,
        COUNT(*) AS refunds,
        SUM(stars) AS refunded_stars
    FROM support_payments
    WHERE refunded_at IS NOT NULL
    GROUP BY 1
)
SELECT
    to_char(m.month, 'YYYY-MM'),
    COALESCE(p.payments, 0),
    COALESCE(p.stars, 0),
    COALESCE(r.refunds, 0),
    COALESCE(r.refunded_stars, 0),
    COALESCE(p.boosts, 0)
FROM months m
LEFT JOIN paid p ON p.month = m.month
LEFT JOIN refunded r ON r.month = m.month
ORDER BY m.month`

// creditsSQL sums the unused bonus credits of users and groups.
const creditsSQL = `SELECT
    (SELECT COUNT(*) FROM gated_fetch_credits WHERE credits > 0),
    (SELECT COALESCE(SUM(credits), 0) FROM gated_fetch_credits),
    (SELECT COUNT(*) FROM gated_fetch_chat_credits WHERE credits > 0),
    (SELECT COALESCE(SUM(credits), 0) FROM gated_fetch_chat_credits)`

// reportTotalsSQL counts all reports by their resolved flag.
const reportTotalsSQL = `SELECT
    COUNT(*) FILTER (WHERE resolved = false),
    COUNT(*) FILTER (WHERE resolved = true)
FROM reports`

// reportsWeeklySQL: $1 timezone, $2 now, $3 weeks (Monday weeks).
const reportsWeeklySQL = `WITH weeks AS (
    SELECT gs::date AS week
    FROM generate_series(
        date_trunc('week', $2::timestamptz AT TIME ZONE $1::text) - make_interval(weeks => $3::int - 1),
        date_trunc('week', $2::timestamptz AT TIME ZONE $1::text),
        INTERVAL '1 week'
    ) AS gs
),
counts AS (
    SELECT
        date_trunc('week', created_at AT TIME ZONE $1::text)::date AS week,
        COUNT(*) FILTER (WHERE resolved = false) AS open_count,
        COUNT(*) FILTER (WHERE resolved = true) AS resolved_count
    FROM reports
    GROUP BY 1
)
SELECT to_char(w.week, 'YYYY-MM-DD'), COALESCE(c.open_count, 0), COALESCE(c.resolved_count, 0)
FROM weeks w
LEFT JOIN counts c ON c.week = w.week
ORDER BY w.week`

// reportColumns are the columns every report query returns.
const reportColumns = `id,
    created_at,
    user_id,
    COALESCE(username, ''),
    COALESCE(first_name, ''),
    chat_id,
    COALESCE(chat_title, ''),
    message,
    resolved,
    resolved_at`

// latestReportsSQL: $1 limit, $2 resolved flag to keep (NULL keeps all).
const latestReportsSQL = `SELECT ` + reportColumns + `
FROM reports
WHERE $2::boolean IS NULL OR resolved = $2::boolean
ORDER BY created_at DESC NULLS LAST, id DESC
LIMIT $1::int`

// reportSQL: $1 id.
const reportSQL = `SELECT ` + reportColumns + `
FROM reports
WHERE id = $1::int`

// replyColumns are the columns every report reply query returns, after its
// report_id.
const replyColumns = `id, text, source, status, resolve_on_send, attempts, next_attempt_at, error, created_at, sent_at`

// reportRepliesSQL lists the replies of the given reports, oldest first:
// $1 report ids.
const reportRepliesSQL = `SELECT report_id, ` + replyColumns + `
FROM report_replies
WHERE report_id = ANY($1::int[])
ORDER BY created_at NULLS FIRST, id`

// queueReportReplySQL queues a reply for the bot's outbox; no row when the
// report does not exist: $1 report id, $2 text, $3 source, $4 status,
// $5 resolve the report once the bot has delivered it.
const queueReportReplySQL = `INSERT INTO report_replies (report_id, text, source, status, resolve_on_send)
SELECT id, $2::text, $3::text, $4::text, $5::boolean
FROM reports
WHERE id = $1::int
RETURNING report_id, ` + replyColumns

// setReportResolvedSQL sets the flag and stamps resolved_at (keeping the
// time of a report already resolved) or clears it: $1 id, $2 resolved.
const setReportResolvedSQL = `UPDATE reports
SET resolved = $2::boolean,
    resolved_at = CASE WHEN $2::boolean THEN COALESCE(resolved_at, now()) ELSE NULL END
WHERE id = $1::int
RETURNING ` + reportColumns

// blogPostColumns are the columns every blog post query returns.
const blogPostColumns = `id, title, content, published, created_at, updated_at`

// blogPostsSQL lists every post, newest first like the bot's owner panel.
const blogPostsSQL = `SELECT ` + blogPostColumns + `
FROM blog_posts
ORDER BY created_at DESC NULLS LAST, id DESC`

// blogPostSQL: $1 id.
const blogPostSQL = `SELECT ` + blogPostColumns + `
FROM blog_posts
WHERE id = $1::int`

// createBlogPostSQL inserts an unpublished post: $1 title, $2 content.
const createBlogPostSQL = `INSERT INTO blog_posts (title, content)
VALUES ($1::text, $2::text)
RETURNING ` + blogPostColumns

// updateBlogPostSQL edits a post only when it still has the updated_at the
// editor loaded (NULL matches NULL); the trigger sets the new updated_at.
// $1 id, $2 title, $3 content, $4 loaded updated_at.
const updateBlogPostSQL = `UPDATE blog_posts
SET title = $2::text, content = $3::text
WHERE id = $1::int AND updated_at IS NOT DISTINCT FROM $4::timestamptz
RETURNING ` + blogPostColumns

// blogPostExistsSQL tells a changed post from a missing one: $1 id.
const blogPostExistsSQL = `SELECT EXISTS (SELECT 1 FROM blog_posts WHERE id = $1::int)`

// setBlogPostPublishedSQL sets the flag without touching the text, so a
// concurrent edit in Telegram is kept: $1 id, $2 published.
const setBlogPostPublishedSQL = `UPDATE blog_posts
SET published = $2::boolean
WHERE id = $1::int
RETURNING ` + blogPostColumns

// deleteBlogPostSQL: $1 id.
const deleteBlogPostSQL = `DELETE FROM blog_posts
WHERE id = $1::int
RETURNING id`
