// Dev-only fixtures, served when the dev server runs with VITE_MOCK=1
// (`npm run dev:mock`). Never part of the production bundle: api.ts imports
// this module only behind import.meta.env.DEV.
//
// Append ?mock=empty or ?mock=error to the page URL (before the #) to check
// the empty and error states; ?mock=conflict makes the first save of each blog
// post answer 409, as if the post had been edited in Telegram meanwhile.
// Blog writes change an in-memory store that lasts until the page reloads.

import type {
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
} from './api';
import { ApiError, HTTP_CONFLICT, HTTP_NOT_FOUND } from './api';
import { titleLength, TITLE_MAX_CHARS } from './editing';
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

type Mode = 'data' | 'empty' | 'error' | 'conflict';
const MODES: Mode[] = ['data', 'empty', 'error', 'conflict'];

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
      { scope: 'chat', scope_id: -1_001_234_567_000, title: 'Meme Lab', used: 17, limit: 20, custom: false },
      { scope: 'chat', scope_id: -1_001_234_567_001, title: 'Kyiv cyclists', used: 9, limit: 40, custom: true },
      { scope: 'user', scope_id: 100_200, title: 'olena_k', used: 6, limit: null, custom: false },
      { scope: 'chat', scope_id: -1_001_234_567_005, title: '', used: 2, limit: null, custom: false },
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

function reports(limit: number, weeks: number, empty: boolean): ReportsResponse {
  const longText = 'The video from this TikTok link came without sound. I tried twice, the second time the bot sent the same file. '
    + 'Other links in the same chat work fine, so it is probably something about this particular post. '
    + 'It is a slideshow with music, if that helps. <b>Not bold</b> & not HTML either.\nSecond line of the report.';

  return {
    ...meta(),
    limit,
    weeks,
    totals: { open: empty ? 0 : 2, resolved: empty ? 0 : 5 },
    weekly: weeksBack(weeks).map((week, index) => ({
      week,
      open: ((empty === false) && (index >= weeks - 2)) ? 1 : 0,
      resolved: ((empty === false) && ((index % 5) === 0)) ? 1 : 0,
    })),
    latest: empty ? [] : [
      { id: 7, created_at: hoursAgo(2), user_id: 100_200, username: 'olena_k', first_name: 'Olena', chat_id: -1_001_234_567_000, chat_title: 'Meme Lab', message: longText, resolved: false },
      { id: 6, created_at: hoursAgo(30), user_id: 100_205, username: '', first_name: 'Dmytro', chat_id: 100_205, chat_title: '', message: 'Instagram story link returns nothing.', resolved: false },
      { id: 5, created_at: hoursAgo(200), user_id: 100_201, username: 'max_dev', first_name: 'Max', chat_id: -1_001_234_567_003, chat_title: 'Dev chat 🛠', message: 'Reddit gallery only shows the first image.', resolved: true },
    ],
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
    reports: () => reports(query.limit ?? 50, query.weeks ?? 26, empty),
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
    content: '💚 You can now <b>support the bot with Telegram Stars</b>.\n\nSend <code>/donate</code> and pick a tier:\n<pre><code class="language-text">25 ⭐  +10 bonus downloads\n50 ⭐  +25 bonus downloads</code></pre>\nBonus downloads unlock restricted Instagram posts &amp; never expire.',
  },
  {
    title: 'Changelog dump (too long for one message)',
    content: `<b>Everything that changed this year</b>\n\n${'• Fixed one more edge case in link detection &amp; retries.\n'.repeat(80)}`,
  },
  {
    title: 'October update: Threads and YouTube posts',
    content: '📢 <b>What’s new</b>\n\n• <b>Threads</b> links now come back as real photos and videos.\n• <i>YouTube community posts</i> are supported.\n• Reddit works again, even when Reddit blocks us.\n\n<blockquote expandable>Restricted Instagram posts use a monthly quota per chat. When it runs out, group admins can raise it with Stars: send /boost in the group.</blockquote>\n\nOne more thing: <tg-spoiler>the bot now has a Report a bug button under posts</tg-spoiler>.\n\nQuestions? <a href="https://t.me/example">Write to us</a>.',
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

/** Answers the blog endpoints like the server, including 400/404/409. */
export async function mockRequest(method: string, path: string, body: unknown, signal?: AbortSignal): Promise<unknown> {
  await wait(signal);

  if (mockMode() === 'error') {
    throw new ApiError(500, 'internal error');
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
