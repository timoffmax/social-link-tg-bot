// Typed client for the dashboard API. The shapes mirror the server's JSON
// exactly (see the build spec); every response carries generated_at and timezone.

/** "YYYY-MM-DD" in the dashboard timezone. */
export type Day = string;
/** "YYYY-MM" in the dashboard timezone (gated usage: UTC month start). */
export type Month = string;
/** RFC3339 timestamp. */
export type Timestamp = string;

export interface ResponseMeta {
  generated_at: Timestamp;
  timezone: string;
}

export interface OverviewTotals {
  downloads_all: number;
  downloads_range: number;
  downloads_7d: number;
  downloads_30d: number;
  active_users_7d: number;
  active_users_30d: number;
  active_chats_7d: number;
  active_chats_30d: number;
  groups_all: number;
  private_users_all: number;
}

export interface OverviewDaily {
  day: Day;
  private: number;
  group: number;
}

export interface PlatformMixRow {
  platform: string;
  downloads: number;
}

export interface NewChatsWeek {
  week: Day;
  groups: number;
  private: number;
}

export interface OverviewResponse extends ResponseMeta {
  days: number;
  totals: OverviewTotals;
  daily: OverviewDaily[];
  platform_mix: PlatformMixRow[];
  new_chats_weekly: NewChatsWeek[];
}

export interface PlatformDaily {
  day: Day;
  counts: Record<string, number>;
}

export interface MediaTypeRow {
  platform: string;
  photo: number;
  video: number;
}

export type HealthStatus = 'ok' | 'drop' | 'stale';

export interface PlatformHealth {
  platform: string;
  last_download_at: Timestamp | null;
  downloads_24h: number;
  downloads_7d: number;
  avg_daily_prev_28d: number;
  avg_daily_7d: number;
  change_pct: number | null;
  status: HealthStatus;
}

export interface PlatformsResponse extends ResponseMeta {
  days: number;
  platforms: string[];
  daily: PlatformDaily[];
  media_types: MediaTypeRow[];
  health: PlatformHealth[];
}

export interface TopGroup {
  chat_id: number;
  title: string;
  downloads: number;
  users: number;
  last_at: Timestamp | null;
}

export interface TopUser {
  user_id: number;
  username: string;
  downloads: number;
  last_at: Timestamp | null;
}

export interface Retention {
  month: Month;
  active_this_month: number;
  active_last_month: number;
  retained: number;
}

export interface ChatSetting {
  type: string;
  key: string;
  value: string;
  enabled: boolean;
}

export interface CustomSettingsChat {
  chat_id: number;
  title: string;
  daily_limit: number;
  timezone: string;
  user_limits: number;
  settings: ChatSetting[];
}

export interface ChatsResponse extends ResponseMeta {
  days: number;
  limit: number;
  top_groups: TopGroup[];
  top_users: TopUser[];
  retention: Retention;
  custom_settings: CustomSettingsChat[];
}

export interface GatedMonth {
  month: Month;
  global_used: number;
  chats_used: number;
  users_used: number;
}

export type GatedScope = 'chat' | 'user';

export interface GatedCurrent {
  scope: GatedScope;
  scope_id: number;
  title: string;
  used: number;
  /** The custom monthly limit; null for the default limit and when unlimited. */
  limit: number | null;
  unlimited: boolean;
  custom: boolean;
}

export interface StarsMonth {
  month: Month;
  payments: number;
  stars: number;
  refunds: number;
  refunded_stars: number;
  boosts: number;
}

export interface Credits {
  users_with_credits: number;
  user_credits: number;
  chats_with_credits: number;
  chat_credits: number;
}

export interface MoneyResponse extends ResponseMeta {
  months: number;
  gated_monthly: GatedMonth[];
  gated_current: GatedCurrent[];
  stars_monthly: StarsMonth[];
  credits: Credits;
}

export interface ReportTotals {
  open: number;
  resolved: number;
}

export interface ReportWeek {
  week: Day;
  open: number;
  resolved: number;
}

export interface Report {
  id: number;
  created_at: Timestamp | null;
  user_id: number;
  username: string;
  first_name: string;
  chat_id: number;
  chat_title: string;
  message: string;
  resolved: boolean;
}

export interface ReportsResponse extends ResponseMeta {
  limit: number;
  weeks: number;
  totals: ReportTotals;
  weekly: ReportWeek[];
  latest: Report[];
}

export interface BlogPost {
  id: number;
  title: string;
  /** Telegram HTML, sent by the owner panel's Broadcast as the message text (the title is not sent). */
  content: string;
  published: boolean;
  created_at: Timestamp | null;
  updated_at: Timestamp | null;
  /** Telegram length of the content: UTF-16 code units after entity parsing. */
  length: number;
}

export interface BlogPostsResponse extends ResponseMeta {
  posts: BlogPost[];
}

export interface BlogPostResponse {
  post: BlogPost;
}

export interface BlogPostDraft {
  title: string;
  content: string;
}

export interface BlogPostUpdate extends BlogPostDraft {
  /** The updated_at the editor loaded (null for a never-edited row): a newer row answers 409 "changed elsewhere". */
  updated_at: Timestamp | null;
}

export interface ApiErrorBody {
  error: string;
}

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

export const RANGE_DAYS = [7, 30, 90, 180, 365] as const;
export type RangeDays = (typeof RANGE_DAYS)[number];
export const DEFAULT_RANGE_DAYS: RangeDays = 90;

export const HTTP_NOT_FOUND = 404;
export const HTTP_CONFLICT = 409;
const HTTP_NO_CONTENT = 204;

export const CHATS_LIMIT = 20;
export const MONEY_MONTHS = 12;
export const REPORTS_LIMIT = 50;
export const REPORTS_WEEKS = 26;

type Query = Record<string, number>;

function buildPath(endpoint: string, query: Query): string {
  const params = new URLSearchParams();

  for (const [key, value] of Object.entries(query)) {
    params.set(key, String(value));
  }

  return `/api/${endpoint}?${params.toString()}`;
}

function isErrorBody(value: unknown): value is ApiErrorBody {
  const isObject = (typeof value === 'object') && (value !== null);

  return isObject && (typeof (value as { error?: unknown }).error === 'string');
}

async function readError(response: Response): Promise<ApiError> {
  let message = `Request failed with HTTP ${response.status}`;

  try {
    const body: unknown = await response.json();

    if (isErrorBody(body)) {
      message = body.error;
    }
  } catch {
    // Not JSON (e.g. a proxy error page): keep the status message.
  }

  return new ApiError(response.status, message);
}

// Dev-only fixtures: import.meta.env.DEV is false in production builds, so
// this constant folds to false and the mock module is dropped from the bundle.
const IS_MOCK = import.meta.env.DEV && (import.meta.env.VITE_MOCK === '1');

async function fetchJson<T>(endpoint: string, query: Query, signal?: AbortSignal): Promise<T> {
  if (IS_MOCK) {
    const mock = await import('./mock');

    return mock.mockResponse(endpoint, query, signal) as Promise<T>;
  }

  const response = await fetch(buildPath(endpoint, query), {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
    signal,
  });

  if (response.ok === false) {
    throw await readError(response);
  }

  return (await response.json()) as T;
}

export function getOverview(days: number, signal?: AbortSignal): Promise<OverviewResponse> {
  return fetchJson<OverviewResponse>('overview', { days }, signal);
}

export function getPlatforms(days: number, signal?: AbortSignal): Promise<PlatformsResponse> {
  return fetchJson<PlatformsResponse>('platforms', { days }, signal);
}

export function getChats(days: number, limit: number, signal?: AbortSignal): Promise<ChatsResponse> {
  return fetchJson<ChatsResponse>('chats', { days, limit }, signal);
}

export function getMoney(months: number, signal?: AbortSignal): Promise<MoneyResponse> {
  return fetchJson<MoneyResponse>('money', { months }, signal);
}

export function getReports(limit: number, weeks: number, signal?: AbortSignal): Promise<ReportsResponse> {
  return fetchJson<ReportsResponse>('reports', { limit, weeks }, signal);
}

export type WriteMethod = 'POST' | 'PUT' | 'DELETE';
type Method = 'GET' | WriteMethod;

// Every write carries the header and JSON content type the server's write
// protection requires; the browser adds Origin. No CORS: same origin only.
const WRITE_HEADERS: Record<string, string> = {
  Accept: 'application/json',
  'Content-Type': 'application/json',
  'X-Dashboard-Request': '1',
};

async function request<T>(method: Method, path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  if (IS_MOCK) {
    const mock = await import('./mock');

    return mock.mockRequest(method, path, body, signal) as Promise<T>;
  }

  const isWrite = (method !== 'GET');
  const response = await fetch(path, {
    method,
    headers: isWrite ? WRITE_HEADERS : { Accept: 'application/json' },
    body: (body === undefined) ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
    signal,
  });

  if (response.ok === false) {
    throw await readError(response);
  }

  const result = (response.status === HTTP_NO_CONTENT) ? undefined : await response.json();

  return result as T;
}

const BLOG_PATH = '/api/blog-posts';

function blogPostPath(id: number): string {
  return `${BLOG_PATH}/${id}`;
}

export function getBlogPosts(signal?: AbortSignal): Promise<BlogPostsResponse> {
  return request<BlogPostsResponse>('GET', BLOG_PATH, undefined, signal);
}

export function getBlogPost(id: number, signal?: AbortSignal): Promise<BlogPostResponse> {
  return request<BlogPostResponse>('GET', blogPostPath(id), undefined, signal);
}

export function createBlogPost(draft: BlogPostDraft): Promise<BlogPostResponse> {
  return request<BlogPostResponse>('POST', BLOG_PATH, draft);
}

export function updateBlogPost(id: number, update: BlogPostUpdate): Promise<BlogPostResponse> {
  return request<BlogPostResponse>('PUT', blogPostPath(id), update);
}

export function setBlogPostPublished(id: number, published: boolean): Promise<BlogPostResponse> {
  return request<BlogPostResponse>('PUT', `${blogPostPath(id)}/published`, { published });
}

export function deleteBlogPost(id: number): Promise<void> {
  return request<void>('DELETE', blogPostPath(id), undefined);
}
