// Dev-only fixtures, served when the dev server runs with VITE_MOCK=1
// (`npm run dev:mock`). Never part of the production bundle: api.ts imports
// this module only behind import.meta.env.DEV.
//
// Append ?mock=empty or ?mock=error to the page URL (before the #) to check
// the empty and error states; ?mock=conflict makes the first save of each blog
// post answer 409, as if the post had been edited in Telegram meanwhile.
// Blog and report writes change in-memory stores that last until the page
// reloads. A queued reply is "sent" a few seconds later (resolving its report
// when it was queued with resolve), except to Dmytro (report #6), who has
// blocked the bot, so it fails.
// Settings changes are applied by a pretend bot about 3 seconds after they are
// queued; the first change of the bot-wide special-downloads cap is rejected as
// "changed meanwhile, reload", and the Reddit proxy has a change queued from
// another tab at load, so changing it answers 409 until that one is applied.
// Changes that need the owner's approval wait for it about 10 seconds and are
// then approved, except a lower HikerAPI price, which the owner rejects, and a
// new vxreddit host, which nobody approves, so it expires after 30 seconds.
// At most 3 changes wait for approval at once; a fourth is rejected, as the
// bot does. ?mock=noadmin shows ADMIN_USER_ID as not set, so changes that
// need approval are refused.
// ?mock=stale shows the bot as not running and a failed profile push.

import type {
  Report,
  ReportReply,
  ReportReplyRequest,
  ReportReplyResponse,
  ReportResponse,
  ReportStatusFilter,
  BlogPost,
  BlogPostDraft,
  BlogPostResponse,
  BlogPostsResponse,
  BlogPostUpdate,
  ChatsResponse,
  MoneyResponse,
  OverviewResponse,
  PlatformsResponse,
  ReportsResponse,
  AuditEntry,
  AuditResponse,
  SettingChangeDraft,
  SettingRequest,
  SettingRequestResponse,
  SettingRow,
  SettingsResponse,
  SettingValue,
  StoredSetting,
} from './api';
import { ApiError, HTTP_CONFLICT, HTTP_NOT_FOUND } from './api';
import { titleLength, TITLE_MAX_CHARS } from './editing';
import { needsApproval, sameValue, validateValue } from './settings';
import { parseTelegramHtml, TELEGRAM_TEXT_LIMIT } from './telegramHtml';

const TIMEZONE = 'Europe/Kyiv';
const DAY_MS = 86_400_000;
const LATENCY_MS = 450;
const PLATFORMS = ['tiktok', 'instagram', 'twitter', 'youtube', 'reddit', 'threads', 'facebook', '9gag', 'unknown'];
const PLATFORM_WEIGHT: Record<string, number> = {
  tiktok: 18,
  instagram: 14,
  twitter: 7,
  youtube: 5,
  reddit: 3,
  threads: 1.6,
  facebook: 0.9,
  '9gag': 0.25,
  unknown: 0.1,
};

type Mode = 'data' | 'empty' | 'error' | 'conflict' | 'stale' | 'noadmin';
const MODES: Mode[] = ['data', 'empty', 'error', 'conflict', 'stale', 'noadmin'];

function isMode(value: string | null): value is Mode {
  return MODES.includes(value as Mode);
}

function mockMode(): Mode {
  const value = new URLSearchParams(window.location.search).get('mock');
  const result: Mode = isMode(value) ? value : 'data';

  return result;
}

// Deterministic pseudo-random numbers so reloads look the same.
function seeded(seed: number): () => number {
  let state = seed;

  return () => {
    state = ((state * 1_103_515_245) + 12_345) % 2_147_483_648;

    return state / 2_147_483_648;
  };
}

function isoDay(time: number): string {
  return new Date(time).toISOString().slice(0, 10);
}

function todayStart(): number {
  const today = new Intl.DateTimeFormat('en-CA', { timeZone: TIMEZONE }).format(new Date());

  return Date.parse(`${today}T00:00:00Z`);
}

function daysBack(count: number): string[] {
  const end = todayStart();
  const result: string[] = [];

  for (let index = count - 1; index >= 0; index--) {
    result.push(isoDay(end - (index * DAY_MS)));
  }

  return result;
}

function weeksBack(count: number): string[] {
  const end = todayStart();
  const weekday = (new Date(end).getUTCDay() + 6) % 7;
  const monday = end - (weekday * DAY_MS);
  const result: string[] = [];

  for (let index = count - 1; index >= 0; index--) {
    result.push(isoDay(monday - (index * 7 * DAY_MS)));
  }

  return result;
}

function monthsBack(count: number): string[] {
  const now = new Date(todayStart());
  const result: string[] = [];

  for (let index = count - 1; index >= 0; index--) {
    const date = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - index, 1));
    result.push(date.toISOString().slice(0, 7));
  }

  return result;
}

function hoursAgo(hours: number): string {
  return new Date(Date.now() - (hours * 3_600_000)).toISOString();
}

function meta() {
  return { generated_at: new Date().toISOString(), timezone: TIMEZONE };
}

function dailyCount(random: () => number, base: number, dayIndex: number): number {
  const weekly = 1 + (0.25 * Math.sin(dayIndex / 1.1));
  const growth = 0.7 + (dayIndex / 600);

  return Math.max(0, Math.round(base * weekly * growth * (0.55 + random())));
}

function sum(values: number[]): number {
  return values.reduce((total, value) => total + value, 0);
}

function overview(days: number, empty: boolean): OverviewResponse {
  const random = seeded(7);
  const dayList = daysBack(days);
  const daily = dayList.map((day, index) => ({
    day,
    private: empty ? 0 : dailyCount(random, 14, index),
    group: empty ? 0 : dailyCount(random, 38, index),
  }));
  const rangeTotal = sum(daily.map((row) => row.private + row.group));
  const lastDays = (count: number) => sum(daily.slice(-count).map((row) => row.private + row.group));
  const weightTotal = sum(Object.values(PLATFORM_WEIGHT));
  const platformMix = PLATFORMS.map((platform) => ({
    platform,
    downloads: Math.round((rangeTotal * (PLATFORM_WEIGHT[platform] ?? 0)) / weightTotal),
  }));
  const weekList = weeksBack(Math.ceil(days / 7));
  const newChats = weekList.map((week) => ({
    week,
    groups: empty ? 0 : Math.round(random() * 4),
    private: empty ? 0 : Math.round(random() * 9),
  }));

  return {
    ...meta(),
    days,
    totals: {
      downloads_all: empty ? 0 : 48_213,
      downloads_range: rangeTotal,
      downloads_7d: lastDays(7),
      downloads_30d: lastDays(30),
      active_users_7d: empty ? 0 : 143,
      active_users_30d: empty ? 0 : 412,
      active_chats_7d: empty ? 0 : 61,
      active_chats_30d: empty ? 0 : 118,
      groups_all: empty ? 0 : 236,
      private_users_all: empty ? 0 : 1_874,
    },
    daily,
    platform_mix: empty ? [] : platformMix,
    new_chats_weekly: newChats,
  };
}

function platforms(days: number, empty: boolean): PlatformsResponse {
  const random = seeded(11);
  const dayList = daysBack(days);
  const daily = dayList.map((day, index) => {
    const counts: Record<string, number> = {};

    for (const platform of PLATFORMS) {
      const base = PLATFORM_WEIGHT[platform] ?? 0;
      const dropFactor = ((platform === 'twitter') && (index > days - 8)) ? 0.3 : 1;
      const staleFactor = ((platform === 'facebook') && (index > days - 4)) ? 0 : 1;
      counts[platform] = empty ? 0 : Math.round(dailyCount(random, base * 2.2, index) * dropFactor * staleFactor);
    }

    return { day, counts };
  });
  const mediaTypes = PLATFORMS.map((platform) => {
    const total = sum(daily.map((row) => row.counts[platform] ?? 0));
    const photoShare = (platform === 'instagram') ? 0.55 : ((platform === 'reddit') ? 0.4 : 0.12);

    return { platform, photo: Math.round(total * photoShare), video: Math.round(total * (1 - photoShare)) };
  });

  return {
    ...meta(),
    days,
    platforms: empty ? [] : PLATFORMS,
    daily: empty ? daily.map((row) => ({ day: row.day, counts: {} })) : daily,
    media_types: empty ? [] : mediaTypes,
    health: empty ? [] : [
      { platform: 'tiktok', last_download_at: hoursAgo(0.2), downloads_24h: 41, downloads_7d: 280, avg_daily_prev_28d: 38.1, avg_daily_7d: 40, change_pct: 5, status: 'ok' },
      { platform: 'instagram', last_download_at: hoursAgo(0.6), downloads_24h: 30, downloads_7d: 214, avg_daily_prev_28d: 29.4, avg_daily_7d: 30.6, change_pct: 4.1, status: 'ok' },
      { platform: 'twitter', last_download_at: hoursAgo(3), downloads_24h: 4, downloads_7d: 33, avg_daily_prev_28d: 14.8, avg_daily_7d: 4.7, change_pct: -68.2, status: 'drop' },
      { platform: 'youtube', last_download_at: hoursAgo(1.5), downloads_24h: 9, downloads_7d: 77, avg_daily_prev_28d: 10.2, avg_daily_7d: 11, change_pct: 7.8, status: 'ok' },
      { platform: 'reddit', last_download_at: hoursAgo(5), downloads_24h: 6, downloads_7d: 44, avg_daily_prev_28d: 6.6, avg_daily_7d: 6.3, change_pct: -4.5, status: 'ok' },
      { platform: 'facebook', last_download_at: hoursAgo(76), downloads_24h: 0, downloads_7d: 4, avg_daily_prev_28d: 1.9, avg_daily_7d: 0.6, change_pct: -68.4, status: 'stale' },
      { platform: 'threads', last_download_at: hoursAgo(9), downloads_24h: 2, downloads_7d: 23, avg_daily_prev_28d: 3.1, avg_daily_7d: 3.3, change_pct: 6.5, status: 'ok' },
      { platform: '9gag', last_download_at: hoursAgo(130), downloads_24h: 0, downloads_7d: 1, avg_daily_prev_28d: 0.4, avg_daily_7d: 0.1, change_pct: -75, status: 'ok' },
      { platform: 'unknown', last_download_at: hoursAgo(300), downloads_24h: 0, downloads_7d: 0, avg_daily_prev_28d: 0, avg_daily_7d: 0, change_pct: null, status: 'ok' },
    ],
  };
}

function chats(days: number, limit: number, empty: boolean): ChatsResponse {
  const groupNames = ['Meme Lab', 'Kyiv cyclists', 'Family', 'Dev chat 🛠', 'Друзі з універу', 'Board games night', 'Weekend hikes', 'Cat pictures only'];
  const userNames = ['olena_k', 'max_dev', '', 'ivan.p', 'travel_anna', 'dmytro', 'nadia_s'];

  return {
    ...meta(),
    days,
    limit,
    top_groups: empty ? [] : groupNames.map((title, index) => ({
      chat_id: -1_001_234_567_000 - index,
      title,
      downloads: Math.round(900 / (index + 1)),
      users: 12 - index,
      last_at: hoursAgo(index * 7),
    })),
    top_users: empty ? [] : userNames.map((username, index) => ({
      user_id: 100_200 + index,
      username,
      downloads: Math.round(340 / (index + 1)),
      last_at: hoursAgo(index * 11),
    })),
    retention: {
      month: monthsBack(1)[0] ?? '',
      active_this_month: empty ? 0 : 96,
      active_last_month: empty ? 0 : 118,
      retained: empty ? 0 : 74,
    },
    custom_settings: empty ? [] : [
      {
        chat_id: -1_001_234_567_000,
        title: 'Meme Lab',
        daily_limit: 50,
        timezone: 'Europe/Kyiv',
        user_limits: 2,
        settings: [
          { type: 'platform', key: 'facebook', value: 'false', enabled: false },
        ],
      },
      {
        chat_id: -1_001_234_567_003,
        title: 'Dev chat 🛠',
        daily_limit: 0,
        timezone: 'UTC',
        user_limits: 0,
        settings: [
          { type: 'description', key: 'twitter', value: 'false', enabled: true },
        ],
      },
      {
        chat_id: 100_203,
        title: '',
        daily_limit: 0,
        timezone: 'UTC',
        user_limits: 1,
        settings: [],
      },
    ],
  };
}

function money(months: number, empty: boolean): MoneyResponse {
  const random = seeded(23);
  const monthList = monthsBack(months);

  return {
    ...meta(),
    months,
    gated_monthly: monthList.map((month, index) => {
      const active = (empty === false) && (index >= months - 5);
      const chatsUsed = active ? Math.round(random() * 40) : 0;
      const usersUsed = active ? Math.round(random() * 18) : 0;

      return { month, global_used: chatsUsed + usersUsed, chats_used: chatsUsed, users_used: usersUsed };
    }),
    gated_current: empty ? [] : [
      { scope: 'chat', scope_id: -1_001_234_567_000, title: 'Meme Lab', used: 17, limit: 20, unlimited: false, custom: false },
      { scope: 'chat', scope_id: -1_001_234_567_001, title: 'Kyiv cyclists', used: 9, limit: 40, unlimited: false, custom: true },
      { scope: 'user', scope_id: 100_200, title: 'olena_k', used: 6, limit: null, unlimited: true, custom: true },
      { scope: 'chat', scope_id: -1_001_234_567_005, title: '', used: 2, limit: null, unlimited: false, custom: false },
    ],
    stars_monthly: monthList.map((month, index) => {
      const active = (empty === false) && (index >= months - 3);
      const payments = active ? 1 + Math.round(random() * 3) : 0;

      return {
        month,
        payments,
        stars: payments * 50,
        refunds: (active && (index === months - 2)) ? 1 : 0,
        refunded_stars: (active && (index === months - 2)) ? 25 : 0,
        boosts: active ? Math.round(payments / 2) : 0,
      };
    }),
    credits: {
      users_with_credits: empty ? 0 : 3,
      user_credits: empty ? 0 : 41,
      chats_with_credits: empty ? 0 : 1,
      chat_credits: empty ? 0 : 25,
    },
  };
}

function wait(signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(resolve, LATENCY_MS);

    signal?.addEventListener('abort', () => {
      window.clearTimeout(timer);
      reject(new DOMException('Aborted', 'AbortError'));
    });
  });
}

export async function mockResponse(endpoint: string, query: Record<string, number>, signal?: AbortSignal): Promise<unknown> {
  await wait(signal);

  const mode = mockMode();

  if (mode === 'error') {
    throw new ApiError(500, 'internal error');
  }

  const empty = (mode === 'empty');
  const days = query.days ?? 90;
  const result = ({
    overview: () => overview(days, empty),
    platforms: () => platforms(days, empty),
    chats: () => chats(days, query.limit ?? 20, empty),
    money: () => money(query.months ?? 12, empty),
  } as Record<string, () => unknown>)[endpoint];

  if (result === undefined) {
    throw new ApiError(404, 'not found');
  }

  return result();
}

// Blog posts: an in-memory table with the server's validation and concurrency rules.

const HTTP_BAD_REQUEST = 400;
const BLOG_PREFIX = '/api/blog-posts';

const BLOG_FIXTURES: BlogPostDraft[] = [
  {
    title: 'Old: TikTok outage notice',
    content: '⚠️ <b>TikTok downloads are paused</b>\n\nTikTok blocked the service we use. Links still get a fixed-link reply. We are on it.',
  },
  {
    title: 'Draft: Stars support',
    content: '💚 You can now <b>support the bot with Telegram Stars</b>.\n\nSend <code>/donate</code> and pick a tier:\n<pre><code class="language-text">25 ⭐  +10 bonus downloads\n50 ⭐  +25 bonus downloads</code></pre>\nBonus downloads are extra special downloads &amp; never expire.',
  },
  {
    title: 'Changelog dump (too long for one message)',
    content: `<b>Everything that changed this year</b>\n\n${'• Fixed one more edge case in link detection &amp; retries.\n'.repeat(80)}`,
  },
  {
    title: 'October update: Threads and YouTube posts',
    content: '📢 <b>What’s new</b>\n\n• <b>Threads</b> links now come back as real photos and videos.\n• <i>YouTube community posts</i> are supported.\n• Reddit works again, even when Reddit blocks us.\n\n<blockquote expandable>Special downloads use a monthly limit per chat. When it runs out, group admins can raise it with Stars: send /boost in the group.</blockquote>\n\nOne more thing: <tg-spoiler>the bot now has a Report a bug button under posts</tg-spoiler>.\n\nQuestions? <a href="https://t.me/example">Write to us</a>.',
  },
];

interface BlogStore {
  posts: BlogPost[];
  nextId: number;
  /** Posts whose next content save answers 409 (conflict mode). */
  conflicting: Set<number>;
}

let blogStore: BlogStore | null = null;

function contentLength(content: string): number {
  return parseTelegramHtml(content.trim()).length;
}

function seedBlog(mode: Mode): BlogStore {
  const fixtures = (mode === 'empty') ? [] : BLOG_FIXTURES;
  const posts = fixtures.map((draft, index) => {
    const createdHours = (fixtures.length - index) * 90;

    return {
      id: index + 1,
      title: draft.title,
      content: draft.content,
      published: ((index % 3) === 0),
      created_at: hoursAgo(createdHours),
      updated_at: hoursAgo(createdHours / 3),
      length: contentLength(draft.content),
    };
  });
  const conflicting = new Set((mode === 'conflict') ? posts.map((post) => post.id) : []);

  return { posts, nextId: posts.length + 1, conflicting };
}

function blog(): BlogStore {
  if (blogStore === null) {
    blogStore = seedBlog(mockMode());
  }

  return blogStore;
}

function copyPost(post: BlogPost): BlogPostResponse {
  return { post: { ...post } };
}

function findPost(id: number): BlogPost {
  const result = blog().posts.find((post) => (post.id === id));

  if (result === undefined) {
    throw new ApiError(HTTP_NOT_FOUND, 'not found');
  }

  return result;
}

/** The server's checks, in its order; the first problem is the answer. */
function validateDraft(draft: BlogPostDraft): BlogPostDraft {
  const title = draft.title.trim();
  const content = draft.content.trim();
  const titleChars = titleLength(title);
  const parsed = parseTelegramHtml(content);
  const problem = parsed.problem;
  const checks: [boolean, string][] = [
    [(titleChars === 0), 'title is required'],
    [(titleChars > TITLE_MAX_CHARS), `title is longer than ${TITLE_MAX_CHARS} characters`],
    [(content === ''), 'content is required'],
    [(problem !== null), `content: line ${problem?.line}, column ${problem?.column}: ${problem?.message}`],
    [(parsed.length > TELEGRAM_TEXT_LIMIT), `content is ${parsed.length} characters after formatting; Telegram allows ${TELEGRAM_TEXT_LIMIT}`],
  ];
  const failed = checks.find(([isFailed]) => isFailed);

  if (failed !== undefined) {
    throw new ApiError(HTTP_BAD_REQUEST, failed[1]);
  }

  return { title, content };
}

// A missing timestamp sorts and compares as the epoch.
function timeOf(timestamp: string | null): number {
  const result = (timestamp === null) ? 0 : Date.parse(timestamp);

  return result;
}

function touch(post: BlogPost): void {
  // The trigger sets updated_at on every UPDATE; keep it strictly increasing.
  const now = Math.max(Date.now(), timeOf(post.updated_at) + 1);
  post.updated_at = new Date(now).toISOString();
}

function listPosts(): BlogPostsResponse {
  const posts = [...blog().posts].sort((left, right) => timeOf(right.created_at) - timeOf(left.created_at));

  return { ...meta(), posts: posts.map((post) => ({ ...post })) };
}

function createPost(body: BlogPostDraft): BlogPostResponse {
  const store = blog();
  const draft = validateDraft(body);
  const now = new Date().toISOString();
  const post: BlogPost = {
    id: store.nextId,
    ...draft,
    published: false,
    created_at: now,
    updated_at: now,
    length: contentLength(draft.content),
  };
  store.nextId += 1;
  store.posts.push(post);

  return copyPost(post);
}

function updatePost(id: number, body: BlogPostUpdate): BlogPostResponse {
  const store = blog();
  const post = findPost(id);
  const draft = validateDraft(body);

  if (store.conflicting.has(id)) {
    store.conflicting.delete(id);
    post.content = `${post.content}\n\n<i>(edited in Telegram)</i>`;
    post.length = contentLength(post.content);
    touch(post);
  }

  if (body.updated_at !== post.updated_at) {
    throw new ApiError(HTTP_CONFLICT, 'changed elsewhere');
  }

  post.title = draft.title;
  post.content = draft.content;
  post.length = contentLength(draft.content);
  touch(post);

  return copyPost(post);
}

function publishPost(id: number, body: { published: boolean }): BlogPostResponse {
  const post = findPost(id);
  post.published = body.published;
  touch(post);

  return copyPost(post);
}

function deletePost(id: number): undefined {
  const store = blog();
  findPost(id);
  store.posts = store.posts.filter((post) => (post.id !== id));

  return undefined;
}

type BlogHandler = (id: number, body: unknown) => unknown;

const BLOG_ROUTES: Record<string, BlogHandler> = {
  'GET ': () => listPosts(),
  'POST ': (_id, body) => createPost(body as BlogPostDraft),
  'GET /:id': (id) => copyPost(findPost(id)),
  'PUT /:id': (id, body) => updatePost(id, body as BlogPostUpdate),
  'PUT /:id/published': (id, body) => publishPost(id, body as { published: boolean }),
  'DELETE /:id': (id) => deletePost(id),
};

/** Answers the blog and report endpoints like the server, including 400/404/409. */
export async function mockRequest(method: string, path: string, body: unknown, signal?: AbortSignal): Promise<unknown> {
  await wait(signal);

  if (mockMode() === 'error') {
    throw new ApiError(500, 'internal error');
  }

  if (path.startsWith(REPORTS_PREFIX)) {
    return reportRequest(method, path, body);
  }

  if (path.startsWith(SETTINGS_PREFIX)) {
    return settingsRequest(method, path, body);
  }

  const rest = path.startsWith(BLOG_PREFIX) ? path.slice(BLOG_PREFIX.length) : null;
  const id = Number(/^\/([0-9]+)/.exec(rest ?? '')?.[1] ?? 0);
  const pattern = (rest ?? '').replace(/^\/[0-9]+/, '/:id');
  const handler = (rest === null) ? undefined : BLOG_ROUTES[`${method} ${pattern}`];

  if (handler === undefined) {
    throw new ApiError(HTTP_NOT_FOUND, 'not found');
  }

  return handler(id, body);
}

// Reports: an in-memory table; queued replies move on as the bot's outbox would.

const REPORTS_PREFIX = '/api/reports';
const SEND_DELAY_MS = 6_000;
const BLOCKED_USER_ID = 100_205;
const BLOCKED_ERROR = 'Forbidden: bot was blocked by the user';
const INTERRUPTED_ERROR = 'interrupted, delivery unknown, not resent';

/** The server's reply_prefix and reply_text_limit (the bot's heading), as GET /api/reports returns them. */
const MOCK_REPLY_PREFIX = '💬 <b>Reply from the developer:</b>\n\n';
const MOCK_REPLY_TEXT_LIMIT = TELEGRAM_TEXT_LIMIT - parseTelegramHtml(MOCK_REPLY_PREFIX).length;

const REPLY_ERRORS: Partial<Record<ReportReply['status'], string>> = {
  failed: BLOCKED_ERROR,
  unknown: INTERRUPTED_ERROR,
};

interface ReportStore {
  reports: Report[];
  nextReplyId: number;
  /** When each queued reply was queued, to move it on after SEND_DELAY_MS. */
  queuedAt: Map<number, number>;
}

let reportStore: ReportStore | null = null;

const LONG_REPORT = 'The video from this TikTok link came without sound. I tried twice, the second time the bot sent the same file. '
  + 'Other links in the same chat work fine, so it is probably something about this particular post. '
  + 'It is a slideshow with music, if that helps. <b>Not bold</b> & not HTML either.\nSecond line of the report.';

function reply(id: number, text: string, source: ReportReply['source'], status: ReportReply['status'], hours: number): ReportReply {
  const isSent = (status === 'sent');

  return {
    id,
    text,
    source,
    status,
    resolve_on_send: false,
    attempts: 0,
    next_attempt_at: null,
    error: REPLY_ERRORS[status] ?? null,
    created_at: hoursAgo(hours),
    sent_at: isSent ? hoursAgo(hours) : null,
  };
}

function seedReports(mode: Mode): ReportStore {
  const reports: Report[] = (mode === 'empty') ? [] : [
    { id: 7, created_at: hoursAgo(2), user_id: 100_200, username: 'olena_k', first_name: 'Olena', chat_id: -1_001_234_567_000, chat_title: 'Meme Lab', message: LONG_REPORT, resolved: false, resolved_at: null, replies: [] },
    {
      id: 6, created_at: hoursAgo(30), user_id: BLOCKED_USER_ID, username: '', first_name: 'Dmytro', chat_id: BLOCKED_USER_ID, chat_title: '', message: 'Instagram story link returns nothing.', resolved: false, resolved_at: null,
      replies: [
        reply(2, 'Thanks! Could you send the link again?', 'dashboard', 'failed', 20),
        reply(3, 'Could you send the link once more?', 'dashboard', 'unknown', 10),
      ],
    },
    {
      id: 5, created_at: hoursAgo(200), user_id: 100_201, username: 'max_dev', first_name: 'Max', chat_id: -1_001_234_567_003, chat_title: 'Dev chat 🛠', message: 'Reddit gallery only shows the first image.', resolved: true, resolved_at: hoursAgo(150),
      replies: [reply(1, 'Fixed: <b>galleries</b> now come as albums. Thanks for the report!', 'telegram', 'sent', 150)],
    },
    { id: 4, created_at: hoursAgo(400), user_id: 100_207, username: 'ira', first_name: 'Ira', chat_id: 100_207, chat_title: '', message: 'Threads link sent a fixed link instead of the video.', resolved: true, resolved_at: null, replies: [] },
  ];

  return { reports, nextReplyId: 4, queuedAt: new Map() };
}

function reportsTable(): ReportStore {
  if (reportStore === null) {
    reportStore = seedReports(mockMode());
  }

  return reportStore;
}

/**
 * Moves queued replies on: sent after a few seconds (resolving the report when the reply asked for it,
 * keeping an earlier resolve time), failed for the user who blocked the bot.
 */
function advanceReplies(store: ReportStore): void {
  const now = Date.now();

  for (const report of store.reports) {
    for (const item of report.replies) {
      const queuedAt = store.queuedAt.get(item.id) ?? now;
      const isDue = (item.status === 'queued') && ((now - queuedAt) >= SEND_DELAY_MS);
      const isBlocked = (report.user_id === BLOCKED_USER_ID);

      if (isDue) {
        item.status = isBlocked ? 'failed' : 'sent';
        item.error = isBlocked ? BLOCKED_ERROR : null;
        item.sent_at = isBlocked ? null : new Date(now).toISOString();
      }

      if (isDue && (isBlocked === false) && item.resolve_on_send) {
        report.resolved = true;
        report.resolved_at = report.resolved_at ?? item.sent_at;
      }
    }
  }
}

function copyReport(report: Report): Report {
  return { ...report, replies: report.replies.map((item) => ({ ...item })) };
}

function findReport(id: number): Report {
  const store = reportsTable();
  advanceReplies(store);
  const result = store.reports.find((report) => (report.id === id));

  if (result === undefined) {
    throw new ApiError(HTTP_NOT_FOUND, 'not found');
  }

  return result;
}

function listReports(path: string): ReportsResponse {
  const store = reportsTable();
  advanceReplies(store);
  const params = new URLSearchParams(path.split('?')[1] ?? '');
  const limit = Number(params.get('limit') ?? 50);
  const weeks = Number(params.get('weeks') ?? 26);
  const status = (params.get('status') ?? 'all') as ReportStatusFilter;
  const keep: Record<ReportStatusFilter, (report: Report) => boolean> = {
    all: () => true,
    open: (report) => (report.resolved === false),
    resolved: (report) => report.resolved,
  };
  const open = store.reports.filter(keep.open).length;
  const resolved = store.reports.length - open;
  const isEmpty = (store.reports.length === 0);

  return {
    ...meta(),
    limit,
    weeks,
    status,
    reply_prefix: MOCK_REPLY_PREFIX,
    reply_text_limit: MOCK_REPLY_TEXT_LIMIT,
    totals: { open, resolved },
    weekly: weeksBack(weeks).map((week, index) => ({
      week,
      open: ((isEmpty === false) && (index >= weeks - 2)) ? 1 : 0,
      resolved: ((isEmpty === false) && ((index % 5) === 0)) ? 1 : 0,
    })),
    latest: store.reports.filter(keep[status]).slice(0, limit).map(copyReport),
  };
}

function queueReply(id: number, body: ReportReplyRequest): ReportReplyResponse {
  const store = reportsTable();
  const report = findReport(id);
  const text = body.text.trim();
  const parsed = parseTelegramHtml(text);
  const problem = parsed.problem;
  const checks: [boolean, string][] = [
    [(text === ''), 'text is required'],
    [(problem !== null), `text: line ${problem?.line}, column ${problem?.column}: ${problem?.message}`],
    [(parsed.length > MOCK_REPLY_TEXT_LIMIT), `text is too long: ${parsed.length} characters after formatting, at most ${MOCK_REPLY_TEXT_LIMIT}`],
  ];
  const failed = checks.find(([isFailed]) => isFailed);

  if (failed !== undefined) {
    throw new ApiError(HTTP_BAD_REQUEST, failed[1]);
  }

  const now = new Date().toISOString();
  const item: ReportReply = {
    id: store.nextReplyId,
    text,
    source: 'dashboard',
    status: 'queued',
    resolve_on_send: body.resolve,
    attempts: 0,
    next_attempt_at: null,
    error: null,
    created_at: now,
    sent_at: null,
  };
  store.nextReplyId += 1;
  store.queuedAt.set(item.id, Date.now());
  report.replies.push(item);

  return { ...meta(), reply: { ...item }, report: copyReport(report) };
}

function resolveReport(id: number, body: { resolved: boolean }): ReportResponse {
  const report = findReport(id);
  const resolvedAt = report.resolved_at ?? new Date().toISOString();
  report.resolved = body.resolved;
  report.resolved_at = body.resolved ? resolvedAt : null;

  return { ...meta(), report: copyReport(report) };
}

const REPORT_ROUTES: Record<string, BlogHandler> = {
  'GET /:id': (id) => ({ ...meta(), report: copyReport(findReport(id)) }),
  'POST /:id/replies': (id, body) => queueReply(id, body as ReportReplyRequest),
  'PUT /:id/resolved': (id, body) => resolveReport(id, body as { resolved: boolean }),
};

function reportRequest(method: string, path: string, body: unknown): unknown {
  const rest = path.slice(REPORTS_PREFIX.length);
  const isList = (method === 'GET') && ((rest === '') || rest.startsWith('?'));
  const id = Number(/^\/([0-9]+)/.exec(rest)?.[1] ?? 0);
  const pattern = rest.replace(/^\/[0-9]+/, '/:id');
  const handler = REPORT_ROUTES[`${method} ${pattern}`];

  if (isList) {
    return listReports(path);
  }

  if (handler === undefined) {
    throw new ApiError(HTTP_NOT_FOUND, 'not found');
  }

  return handler(id, body);
}

// Settings: the registry as the bot publishes it, an in-memory bot_settings table, change requests
// the pretend bot decides a few seconds after they are queued, and the audit log of every change.

const SETTINGS_PREFIX = '/api/settings';
const APPLY_DELAY_MS = 3_000;
const OTHER_TAB_DELAY_MS = 8_000;
const APPROVAL_DELAY_MS = 10_000;
const REQUEST_TTL_MS = 10 * 60_000;
const APPROVAL_TTL_MS = 15 * 60_000;
const UNAPPROVED_TTL_MS = 30_000;
const CHANGED_ERROR = 'changed meanwhile, reload';
const OWNER_REJECTED_ERROR = 'rejected by the owner in Telegram';
const APPROVAL_EXPIRED_ERROR = 'expired: the owner did not answer in Telegram within 15 minutes';
const NO_APPROVER_ERROR = 'needs the owner\'s approval, but ADMIN_USER_ID is not set';
const TOO_MANY_AWAITING_ERROR = 'too many changes waiting for approval';
const MAX_AWAITING_APPROVAL = 3;
const QUEUE_EXPIRED_ERROR = 'expired: the bot did not apply it within 10 minutes';
const DASHBOARD_NOTE = 'dashboard request';
const APPROVED_NOTE = 'approved in Telegram';
const REJECTED_ONCE_KEY = 'gated.global_monthly_cap';
const OWNER_REJECTS_KEY = 'paid.hiker.price_per_request_usd';
const NEVER_APPROVED_KEY = 'service.vxreddit.host';

type RegistryEntry = Omit<SettingRow, 'effective' | 'stored' | 'open_request' | 'last_change'>;

function platformEntry(id: string, label: string, envVar: string, impact: string): RegistryEntry {
  return { key: `platform.${id}.enabled`, group: 'platforms', type: 'bool', env_var: envVar, default: true, approval: 'never', label, impact };
}

function proxyEntry(id: string, label: string, envVar: string, defaultValue: boolean): RegistryEntry {
  return {
    key: `proxy.${id}.enabled`,
    group: 'proxies_services',
    type: 'bool',
    env_var: envVar,
    default: defaultValue,
    approval: 'never',
    label,
    impact: 'Off: this platform never uses the proxy pool. Has an effect only while Proxies is on.',
  };
}

function hostEntry(id: string, envVar: string, host: string, label: string): RegistryEntry {
  return {
    key: `service.${id}.host`,
    group: 'proxies_services',
    type: 'host',
    env_var: envVar,
    default: host,
    approval: 'always',
    label,
    impact: 'The bot fetches media from this host and sends it to every chat: a hostname only, no scheme, port, path or IP address.',
  };
}

// Mirrors model/settings/registry.go.
const REGISTRY: RegistryEntry[] = [
  platformEntry('instagram', 'Instagram', 'ENABLE_INSTAGRAM', 'Off: Instagram links are ignored in every chat.'),
  platformEntry('tiktok', 'TikTok', 'ENABLE_TIKTOK', 'Off: TikTok links are ignored in every chat.'),
  platformEntry('twitter', 'Twitter/X', 'ENABLE_TWITTER', 'Off: Twitter/X links are ignored in every chat.'),
  platformEntry('youtube', 'YouTube', 'ENABLE_YOUTUBE', 'Off: YouTube Shorts and community posts are ignored in every chat.'),
  platformEntry('reddit', 'Reddit', 'ENABLE_REDDIT', 'Off: Reddit links are ignored in every chat.'),
  platformEntry('facebook', 'Facebook', 'ENABLE_FACEBOOK', 'Off: Facebook links are ignored in every chat.'),
  platformEntry('9gag', '9GAG', 'ENABLE_9GAG', 'Off: 9GAG links are ignored in every chat.'),
  platformEntry('threads', 'Threads', 'ENABLE_THREADS', 'Off: Threads links are ignored in every chat.'),
  {
    key: 'platform.instagram.fixed_link_service',
    group: 'platforms',
    type: 'enum',
    env_var: 'INSTAGRAM_SERVICE',
    default: 'toinstagram',
    enum: ['toinstagram', 'onebigram'],
    approval: 'never',
    label: 'Instagram fixed-link service',
    impact: 'The domain of the Instagram link the bot sends when it can not download a post.',
  },
  {
    key: 'proxy.enabled',
    group: 'proxies_services',
    type: 'bool',
    env_var: 'ENABLE_PROXIES',
    default: true,
    approval: 'never',
    label: 'Proxies',
    impact: 'Off: no platform uses the proxy pool (the pool itself stays in .env).',
  },
  proxyEntry('youtube', 'YouTube proxy', 'ENABLE_YOUTUBE_PROXY', false),
  proxyEntry('twitter', 'Twitter/X proxy', 'ENABLE_TWITTER_PROXY', true),
  proxyEntry('tiktok', 'TikTok proxy', 'ENABLE_TIKTOK_PROXY', true),
  proxyEntry('facebook', 'Facebook proxy', 'ENABLE_FACEBOOK_PROXY', true),
  proxyEntry('reddit', 'Reddit proxy', 'ENABLE_REDDIT_PROXY', true),
  proxyEntry('9gag', '9GAG proxy', 'ENABLE_9GAG_PROXY', true),
  hostEntry('kkinstagram', 'KKINSTAGRAM_HOST', 'kkinstagram.com', 'kkinstagram host'),
  hostEntry('kktiktok', 'KKTIKTOK_HOST', 'kktiktok.com', 'kktiktok host'),
  hostEntry('vxreddit', 'VXREDDIT_HOST', 'vxreddit.com', 'vxreddit host'),
  hostEntry('facebed', 'FACEBED_HOST', 'www.facebed.com', 'facebed host'),
  {
    key: 'paid.apify.monthly_budget_usd',
    group: 'paid',
    type: 'decimal',
    env_var: 'APIFY_MONTHLY_BUDGET_USD',
    default: 4,
    min: 0,
    ceiling: 20,
    scale: 2,
    approval: 'raise',
    safe_value: 0,
    label: 'Apify monthly budget (USD)',
    impact: 'Real-dollar cap on Apify spend a month (restricted posts, reel videos, stories, YouTube Shorts); 0 turns Apify off.',
  },
  {
    key: 'paid.hiker.monthly_budget_usd',
    group: 'paid',
    type: 'decimal',
    env_var: 'HIKERAPI_MONTHLY_BUDGET_USD',
    default: 1,
    min: 0,
    ceiling: 10,
    scale: 2,
    approval: 'raise',
    safe_value: 0,
    label: 'HikerAPI monthly budget (USD)',
    impact: 'Cap on HikerAPI spend a month, counted by the bot\'s own ledger; 0 turns HikerAPI off.',
  },
  {
    key: 'paid.hiker.price_per_request_usd',
    group: 'paid',
    type: 'decimal',
    env_var: 'HIKERAPI_PRICE_PER_REQUEST_USD',
    default: 0.001,
    min: 0,
    min_exclusive: true,
    max: 1,
    scale: 6,
    approval: 'lower',
    safe_value: 1,
    label: 'HikerAPI price per request (USD)',
    impact: 'What the ledger counts for one billed HikerAPI request; a lower price lets more requests fit in the budget.',
  },
  {
    key: 'paid.instagram.provider_order',
    group: 'paid',
    type: 'ordered_list',
    env_var: 'INSTAGRAM_PAID_PROVIDERS',
    default: ['apify', 'hiker'],
    enum: ['apify', 'hiker'],
    approval: 'never',
    label: 'Paid Instagram providers, in order',
    impact: 'The paid providers tried for restricted posts, reel videos and stories, first to last.',
  },
  {
    key: 'alerts.hiker_low_balance_requests',
    group: 'paid',
    type: 'int',
    env_var: 'HIKERAPI_LOW_BALANCE_REQUESTS',
    default: 100,
    min: 0,
    max: 100_000,
    approval: 'never',
    label: 'HikerAPI low-balance alert (requests)',
    impact: 'The hourly check alerts you when the HikerAPI account has fewer requests left than this.',
  },
  {
    key: 'gated.global_monthly_cap',
    group: 'restricted_posts',
    type: 'int',
    env_var: 'GATED_FETCH_GLOBAL_MONTHLY_CAP',
    default: -1,
    min: -1,
    ceiling: 5000,
    approval: 'never',
    label: 'Special downloads a month, bot-wide',
    impact: '-1: no cap (only the dollar budgets stop the paid ones); 0 turns special downloads off for everyone, the home relay included (Shorts then use the server\'s yt-dlp).',
  },
  {
    key: 'gated.user_monthly_allowance',
    group: 'restricted_posts',
    type: 'int',
    env_var: 'GATED_FETCH_USER_MONTHLY_ALLOWANCE',
    default: 20,
    min: 0,
    ceiling: 1000,
    approval: 'never',
    label: 'Special downloads a month per person (private chats)',
    impact: 'The default monthly allowance in private chats; shown in the bot\'s description and /info.',
  },
  {
    key: 'gated.chat_monthly_cap',
    group: 'restricted_posts',
    type: 'int',
    env_var: 'GATED_FETCH_CHAT_MONTHLY_CAP',
    default: 20,
    min: 0,
    ceiling: 1000,
    approval: 'never',
    label: 'Special downloads a month per group',
    impact: 'The default monthly cap of a group; shown in the bot\'s description and /info.',
  },
  {
    key: 'stars.enabled',
    group: 'stars',
    type: 'bool',
    env_var: 'STARS_SUPPORT_ENABLED',
    default: false,
    approval: 'never',
    safe_value: false,
    label: 'Telegram Stars support',
    impact: 'On: /donate sells Stars support tiers and the Stars commands join the menu. Off: no new payments; bonus downloads already paid keep working.',
  },
  {
    key: 'limits.default_daily_media',
    group: 'limits',
    type: 'int',
    env_var: 'DEFAULT_DAILY_MEDIA_LIMIT',
    default: 0,
    min: 0,
    max: 1000,
    approval: 'never',
    label: 'Default daily media limit per person',
    impact: 'Media a person gets a day where no group admin set a limit; 0 means no limit.',
  },
];

const SETTINGS_GROUPS = ['platforms', 'proxies_services', 'paid', 'restricted_posts', 'stars', 'limits'];

// The .env values of the production inventory; the rest fall back to the default.
const ENV_VALUES: Record<string, SettingValue> = {
  'platform.instagram.enabled': true,
  'platform.tiktok.enabled': true,
  'platform.twitter.enabled': true,
  'platform.youtube.enabled': true,
  'platform.reddit.enabled': true,
  'platform.facebook.enabled': true,
  'platform.9gag.enabled': true,
  'proxy.enabled': false,
  'paid.apify.monthly_budget_usd': 4.8,
  'stars.enabled': true,
};

interface MockRequest {
  request: SettingRequest;
  queuedAt: number;
  delay: number;
  /** When the pretend bot sent the Approve / Reject message; 0 before. */
  askedAt: number;
}

interface SettingsStore {
  stored: Map<string, StoredSetting>;
  requests: Map<number, MockRequest>;
  audit: AuditEntry[];
  nextRequestId: number;
  nextAuditId: number;
  isRejectedOnce: boolean;
}

let settingsStore: SettingsStore | null = null;

function findEntry(key: string): RegistryEntry | undefined {
  return REGISTRY.find((entry) => (entry.key === key));
}

function effectiveOf(store: SettingsStore, entry: RegistryEntry): SettingRow['effective'] {
  const stored = store.stored.get(entry.key);
  const envValue = ENV_VALUES[entry.key];
  const isEnvSet = (envValue !== undefined);
  let result: SettingRow['effective'] = { value: entry.default, source: 'default', env_set: isEnvSet };

  if (isEnvSet) {
    result = { value: envValue, source: 'env', env_set: true };
  }

  if (stored !== undefined) {
    result = { value: stored.value, source: 'db', env_set: isEnvSet };
  }

  return result;
}

function addAudit(store: SettingsStore, key: string, oldValue: SettingValue | null, newValue: SettingValue | null, actor: AuditEntry['actor'], at: string, requestId: number | null, note: string = DASHBOARD_NOTE): void {
  store.audit.unshift({
    id: store.nextAuditId,
    key,
    old_value: oldValue,
    new_value: newValue,
    actor,
    db_role: (actor === 'manual') ? 'postgres' : 'social_link_bot',
    request_id: requestId,
    note: (actor === 'manual') ? null : note,
    created_at: at,
  });
  store.nextAuditId += 1;
}

function newRequest(store: SettingsStore, draft: SettingChangeDraft, createdAt: number, delay: number): SettingRequest {
  const request: SettingRequest = {
    id: store.nextRequestId,
    key: draft.key,
    status: 'queued',
    value: draft.value,
    expected: draft.expected,
    needs_approval: null,
    error: null,
    created_at: new Date(createdAt).toISOString(),
    expires_at: new Date(createdAt + REQUEST_TTL_MS).toISOString(),
    decided_at: null,
  };
  store.nextRequestId += 1;
  store.requests.set(request.id, { request, queuedAt: createdAt, delay, askedAt: 0 });

  return request;
}

function seedSettings(): SettingsStore {
  const store: SettingsStore = { stored: new Map(), requests: new Map(), audit: [], nextRequestId: 40, nextAuditId: 1, isRejectedOnce: false };
  const ninegagAt = hoursAgo(50);
  const limitAt = hoursAgo(120);
  addAudit(store, 'limits.default_daily_media', null, 30, 'manual', limitAt, null);
  store.stored.set('limits.default_daily_media', { value: 30, updated_at: limitAt, updated_by: 'manual' });
  addAudit(store, 'platform.9gag.enabled', null, false, 'dashboard', ninegagAt, 38);
  store.stored.set('platform.9gag.enabled', { value: false, updated_at: ninegagAt, updated_by: 'dashboard' });
  // A change queued from another tab: changing the Reddit proxy answers 409 until it is applied.
  newRequest(store, { key: 'proxy.reddit.enabled', value: false, expected: true }, Date.now(), OTHER_TAB_DELAY_MS);

  return store;
}

function settingsTable(): SettingsStore {
  if (settingsStore === null) {
    settingsStore = seedSettings();
  }

  return settingsStore;
}

function rejectRequest(request: SettingRequest, error: string, decidedAt: string, isApproval: boolean): void {
  request.status = 'rejected';
  request.error = error;
  request.needs_approval = isApproval;
  request.decided_at = decidedAt;
}

function expireRequest(request: SettingRequest, error: string, decidedAt: string): void {
  request.status = 'expired';
  request.error = error;
  request.decided_at = decidedAt;
}

/** Writes the change, its audit row and the request's outcome, as the bot's apply transaction. */
function applyRequest(store: SettingsStore, request: SettingRequest, decidedAt: string, note: string): void {
  const oldValue = store.stored.get(request.key)?.value ?? null;
  request.status = 'applied';
  request.decided_at = decidedAt;
  addAudit(store, request.key, oldValue, request.value, 'dashboard', decidedAt, request.id, note);

  if (request.value === null) {
    store.stored.delete(request.key);
  }

  if (request.value !== null) {
    store.stored.set(request.key, { value: request.value, updated_at: decidedAt, updated_by: 'dashboard' });
  }
}

/** Sends the Approve / Reject message: the request waits for the owner with the approval expiry. */
function askApproval(item: MockRequest, now: number): void {
  const request = item.request;
  const ttl = (request.key === NEVER_APPROVED_KEY) ? UNAPPROVED_TTL_MS : APPROVAL_TTL_MS;
  request.status = 'awaiting_approval';
  request.needs_approval = true;
  request.expires_at = new Date(now + ttl).toISOString();
  item.askedAt = now;
}

/** How many requests wait for the owner's approval. */
function awaitingCount(store: SettingsStore): number {
  const awaiting = [...store.requests.values()].filter((item) => (item.request.status === 'awaiting_approval'));

  return awaiting.length;
}

/** The pretend bot: decides queued requests once their delay has passed, as the real one would. */
function decideRequest(store: SettingsStore, item: MockRequest, now: number): void {
  const request = item.request;
  const entry = findEntry(request.key) as RegistryEntry;
  const current = (effectiveOf(store, entry) as NonNullable<SettingRow['effective']>).value;
  const fallback = ENV_VALUES[entry.key] ?? entry.default;
  const next = request.value ?? fallback;
  const decidedAt = new Date(now).toISOString();
  const isRejectedOnce = (request.key === REJECTED_ONCE_KEY) && (store.isRejectedOnce === false);
  const isChanged = (sameValue(request.expected, current) === false) || isRejectedOnce;
  const isApproval = needsApproval(entry.approval, current, next);
  const isExpired = (now >= Date.parse(request.expires_at));
  const isOpen = (isExpired === false) && (isChanged === false);
  const hasApprover = (mockMode() !== 'noadmin');
  const isFull = (awaitingCount(store) >= MAX_AWAITING_APPROVAL);

  if (isRejectedOnce) {
    store.isRejectedOnce = true;
  }

  if (isExpired) {
    expireRequest(request, QUEUE_EXPIRED_ERROR, decidedAt);
  }

  if ((isExpired === false) && isChanged) {
    rejectRequest(request, CHANGED_ERROR, decidedAt, false);
  }

  if (isOpen && isApproval && (hasApprover === false)) {
    rejectRequest(request, NO_APPROVER_ERROR, decidedAt, true);
  }

  if (isOpen && isApproval && hasApprover && isFull) {
    rejectRequest(request, TOO_MANY_AWAITING_ERROR, decidedAt, true);
  }

  if (isOpen && isApproval && hasApprover && (isFull === false)) {
    askApproval(item, now);
  }

  if (isOpen && (isApproval === false)) {
    request.needs_approval = false;
    applyRequest(store, request, decidedAt, DASHBOARD_NOTE);
  }
}

/** The pretend owner: approves after a few seconds, except the rejected and the never-approved key. */
function decideApproval(store: SettingsStore, item: MockRequest, now: number): void {
  const request = item.request;
  const decidedAt = new Date(now).toISOString();
  const isExpired = (now >= Date.parse(request.expires_at));
  const isAnswered = ((now - item.askedAt) >= APPROVAL_DELAY_MS) && (request.key !== NEVER_APPROVED_KEY);
  const isRejected = (request.key === OWNER_REJECTS_KEY);

  if (isExpired) {
    expireRequest(request, APPROVAL_EXPIRED_ERROR, decidedAt);
  }

  if ((isExpired === false) && isAnswered && isRejected) {
    rejectRequest(request, OWNER_REJECTED_ERROR, decidedAt, true);
  }

  if ((isExpired === false) && isAnswered && (isRejected === false)) {
    applyRequest(store, request, decidedAt, APPROVED_NOTE);
  }
}

function advanceRequests(store: SettingsStore): void {
  const now = Date.now();

  for (const item of store.requests.values()) {
    const isDue = (item.request.status === 'queued') && ((now - item.queuedAt) >= item.delay);
    const isAwaiting = (item.request.status === 'awaiting_approval');

    if (isDue) {
      decideRequest(store, item, now);
    }

    if (isAwaiting) {
      decideApproval(store, item, now);
    }
  }
}

function openRequestOf(store: SettingsStore, key: string): SettingRequest | undefined {
  const open = [...store.requests.values()].find((item) => (item.request.key === key) && ['queued', 'awaiting_approval'].includes(item.request.status));

  return open?.request;
}

function lastChangeOf(store: SettingsStore, key: string): SettingRow['last_change'] {
  const entry = store.audit.find((row) => (row.key === key));
  const result = (entry === undefined) ? null : {
    id: entry.id,
    created_at: entry.created_at,
    actor: entry.actor,
    old_value: entry.old_value,
    new_value: entry.new_value,
  };

  return result;
}

function settingRow(store: SettingsStore, entry: RegistryEntry): SettingRow {
  const open = openRequestOf(store, entry.key);

  return {
    ...entry,
    effective: effectiveOf(store, entry),
    stored: store.stored.get(entry.key) ?? null,
    open_request: (open === undefined) ? null : {
      id: open.id,
      status: open.status,
      value: open.value,
      created_at: open.created_at,
      expires_at: open.expires_at,
    },
    last_change: lastChangeOf(store, entry.key),
  };
}

function listSettings(): SettingsResponse {
  const mode = mockMode();
  const isEmpty = (mode === 'empty');
  const isStale = (mode === 'stale');
  const store = settingsTable();
  advanceRequests(store);
  const heartbeatAt = isStale ? hoursAgo(2) : new Date().toISOString();

  return {
    ...meta(),
    bot: isEmpty ? null : {
      started_at: hoursAgo(26),
      heartbeat_at: heartbeatAt,
      commit: '7f457e1c0ffee',
      commit_modified: false,
      settings_loaded_at: heartbeatAt,
      settings_source: 'db',
      settings_count: store.stored.size,
      is_stale: isStale,
    },
    profile: isEmpty ? null : {
      pushed_at: hoursAgo(26),
      methods: ['setMyCommands', 'setMyDescription', 'setMyShortDescription'],
      error: isStale ? 'Too Many Requests: retry after 30' : undefined,
      pending_at: null,
      attempts: isStale ? 5 : 0,
    },
    secrets: isEmpty ? null : {
      tg_bot_token: true,
      admin_user_id: (mode !== 'noadmin'),
      apify_token: true,
      hiker_token: false,
      proxies: 0,
      relay_url: true,
      relay_token_file: true,
    },
    groups: isEmpty ? [] : SETTINGS_GROUPS,
    platforms: [],
    applying: true,
    settings: isEmpty ? [] : REGISTRY.map((entry) => settingRow(store, entry)),
  };
}

function queueSetting(body: SettingChangeDraft): SettingRequestResponse {
  const store = settingsTable();
  advanceRequests(store);
  const entry = findEntry(body.key);
  const valueError = ((entry === undefined) || (body.value === null)) ? null : validateValue({ ...entry, effective: null, stored: null, open_request: null, last_change: null }, body.value);

  if (entry === undefined) {
    throw new ApiError(HTTP_BAD_REQUEST, `unknown setting "${body.key}"`);
  }

  if (body.expected === undefined) {
    throw new ApiError(HTTP_BAD_REQUEST, 'expected is required');
  }

  if (valueError !== null) {
    throw new ApiError(HTTP_BAD_REQUEST, `value ${valueError}`);
  }

  if (openRequestOf(store, body.key) !== undefined) {
    throw new ApiError(HTTP_CONFLICT, 'a change of this setting is already waiting');
  }

  const request = newRequest(store, body, Date.now(), APPLY_DELAY_MS);

  return { ...meta(), request: { ...request } };
}

function findSettingRequest(id: number): SettingRequestResponse {
  const store = settingsTable();
  advanceRequests(store);
  const item = store.requests.get(id);

  if (item === undefined) {
    throw new ApiError(HTTP_NOT_FOUND, 'not found');
  }

  return { ...meta(), request: { ...item.request } };
}

function listAudit(path: string): AuditResponse {
  const store = settingsTable();
  advanceRequests(store);
  const params = new URLSearchParams(path.split('?')[1] ?? '');
  const key = params.get('key');
  const group = params.get('group');
  const before = Number(params.get('before') ?? 0);
  const limit = Number(params.get('limit') ?? 50);
  const isEmpty = (mockMode() === 'empty');
  const matching = store.audit.filter((row) => {
    const isKey = (key === null) || (row.key === key);
    const isGroup = (group === null) || (findEntry(row.key)?.group === group);
    const isBefore = (before === 0) || (row.id < before);

    return isKey && isGroup && isBefore && (isEmpty === false);
  });
  const entries = matching.slice(0, limit);
  const last = entries[entries.length - 1];
  const hasMore = (matching.length > limit) && (last !== undefined);

  return { ...meta(), entries, next_before: hasMore ? last.id : null };
}

function settingsRequest(method: string, path: string, body: unknown): unknown {
  const rest = path.slice(SETTINGS_PREFIX.length);
  const requestId = /^\/requests\/([0-9]+)$/.exec(rest)?.[1];
  let result: unknown;

  if ((method === 'GET') && (rest === '')) {
    result = listSettings();
  }

  if ((method === 'POST') && (rest === '/requests')) {
    result = queueSetting(body as SettingChangeDraft);
  }

  if ((method === 'GET') && (requestId !== undefined)) {
    result = findSettingRequest(Number(requestId));
  }

  if ((method === 'GET') && rest.startsWith('/audit')) {
    result = listAudit(path);
  }

  if (result === undefined) {
    throw new ApiError(HTTP_NOT_FOUND, 'not found');
  }

  return result;
}
