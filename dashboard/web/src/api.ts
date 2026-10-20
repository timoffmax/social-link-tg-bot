// Typed client for the dashboard API. The shapes mirror the server's JSON
// exactly (see the build spec); every response carries generated_at and timezone.

/** "YYYY-MM-DD" in the dashboard timezone. */
export type Day = string;
/** "YYYY-MM" in the dashboard timezone (special-download usage: UTC month start). */
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

/**
 * queued: waits for the bot's outbox (again, after a passing refusal); sending: claimed by it; sent;
 * failed: refused by Telegram or never sent (error says why); unknown: no answer from Telegram, so it
 * may have been delivered. Failed and unknown replies are never sent again automatically.
 */
export type ReplyStatus = 'queued' | 'sending' | 'sent' | 'failed' | 'unknown';

/** dashboard: queued on this page; telegram: sent with /reply in the bot. */
export type ReplySource = 'dashboard' | 'telegram';

export interface ReportReply {
  id: number;
  /** Telegram HTML, sent below the "Reply from the developer" heading. */
  text: string;
  source: ReplySource;
  status: ReplyStatus;
  /** The bot resolves the report once it has delivered this reply. */
  resolve_on_send: boolean;
  /** Sends refused for a passing reason (429, 5xx) so far. */
  attempts: number;
  /** A queued reply refused for a passing reason is not sent before this. */
  next_attempt_at: Timestamp | null;
  /**
   * Why a reply is failed or unknown (e.g. "Forbidden: bot was blocked by the user"), or the last
   * passing refusal of a queued one.
   */
  error: string | null;
  created_at: Timestamp | null;
  sent_at: Timestamp | null;
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
  /** When it was resolved; null when open and for reports resolved before this was recorded. */
  resolved_at: Timestamp | null;
  /** Oldest first. */
  replies: ReportReply[];
}

export const REPORT_STATUSES = ['open', 'resolved', 'all'] as const;
export type ReportStatusFilter = (typeof REPORT_STATUSES)[number];

export interface ReportsResponse extends ResponseMeta {
  limit: number;
  weeks: number;
  /** The filter of latest; totals and weekly always count every report. */
  status: ReportStatusFilter;
  /** The heading the bot puts above a reply (Telegram HTML); the reply text follows it. */
  reply_prefix: string;
  /** The longest reply text, as Telegram counts it, that fits one message with the heading. */
  reply_text_limit: number;
  totals: ReportTotals;
  weekly: ReportWeek[];
  latest: Report[];
}

export interface ReportResponse extends ResponseMeta {
  report: Report;
}

export interface ReportReplyRequest {
  text: string;
  /** Let the bot resolve the report once the reply is delivered. */
  resolve: boolean;
}

export interface ReportReplyResponse extends ResponseMeta {
  reply: ReportReply;
  report: Report;
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
  /** False when the answer was not the dashboard's JSON error, e.g. a proxy's own refusal page. */
  readonly isFromDashboard: boolean;

  constructor(status: number, message: string, isFromDashboard = true) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.isFromDashboard = isFromDashboard;
  }
}

export const RANGE_DAYS = [7, 30, 90, 180, 365] as const;
export type RangeDays = (typeof RANGE_DAYS)[number];
export const DEFAULT_RANGE_DAYS: RangeDays = 90;

export const HTTP_BAD_REQUEST = 400;
export const HTTP_NOT_FOUND = 404;
export const HTTP_FORBIDDEN = 403;
export const HTTP_CONFLICT = 409;
const HTTP_NO_CONTENT = 204;

export const CHATS_LIMIT = 20;
export const MONEY_MONTHS = 12;
export const REPORTS_LIMIT = 50;
export const REPORTS_WEEKS = 26;

type Query = Record<string, number>;
type PathQuery = Record<string, number | string>;

function buildPath(endpoint: string, query: PathQuery): string {
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
  let isFromDashboard = false;

  try {
    const body: unknown = await response.json();

    if (isErrorBody(body)) {
      message = body.error;
      isFromDashboard = true;
    }
  } catch {
    // Not JSON (e.g. a proxy error page): keep the status message.
  }

  return new ApiError(response.status, message, isFromDashboard);
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

const REPORTS_PATH = '/api/reports';

function reportPath(id: number): string {
  return `${REPORTS_PATH}/${id}`;
}

export function getReports(limit: number, weeks: number, status: ReportStatusFilter, signal?: AbortSignal): Promise<ReportsResponse> {
  const path = buildPath('reports', { limit, weeks, status });

  return request<ReportsResponse>('GET', path, undefined, signal);
}

/** One report with its replies; the Reports tab polls it while a reply waits for the bot. */
export function getReport(id: number, signal?: AbortSignal): Promise<ReportResponse> {
  return request<ReportResponse>('GET', reportPath(id), undefined, signal);
}

/** Queues a reply for the bot to send to the reporter (it never sends from here). */
export function queueReportReply(id: number, reply: ReportReplyRequest): Promise<ReportReplyResponse> {
  return request<ReportReplyResponse>('POST', `${reportPath(id)}/replies`, reply);
}

export function setReportResolved(id: number, resolved: boolean): Promise<ReportResponse> {
  return request<ReportResponse>('PUT', `${reportPath(id)}/resolved`, { resolved });
}

// Settings: the registry and the effective values come from what the bot publishes; the dashboard
// only queues change requests, which the bot validates and applies itself.

export type SettingType = 'bool' | 'int' | 'decimal' | 'enum' | 'host' | 'ordered_list';

/** Which changes wait for the owner's Approve in Telegram; every other change applies directly. */
export type ApprovalRule = 'never' | 'raise' | 'lower' | 'to_true' | 'always';

/**
 * db: set here; cache: the value set here, from the bot's cache file (the database was down at its
 * start); safe: the safe value of a money key whose stored value is no longer valid; env; default.
 */
export type SettingSource = 'db' | 'cache' | 'safe' | 'env' | 'default';

export type SettingActor = 'dashboard' | 'telegram' | 'bot' | 'manual';

export type SettingValue = boolean | number | string | string[];

/** queued and awaiting_approval are open; applied, rejected and expired are final. */
export type SettingRequestStatus = 'queued' | 'awaiting_approval' | 'applied' | 'rejected' | 'expired';

export interface EffectiveSetting {
  value: SettingValue;
  source: SettingSource;
  /** Whether the setting's .env variable is set (its value is not published). */
  env_set: boolean;
}

export interface StoredSetting {
  value: SettingValue;
  updated_at: Timestamp;
  updated_by: SettingActor;
}

export interface OpenSettingRequest {
  id: number;
  status: SettingRequestStatus;
  /** null: a reset to .env / default. */
  value: SettingValue | null;
  created_at: Timestamp;
  expires_at: Timestamp;
}

export interface SettingChange {
  id: number;
  created_at: Timestamp;
  actor: SettingActor;
  old_value: SettingValue | null;
  new_value: SettingValue | null;
}

/** One managed setting: the registry fields as the bot published them, then its state. */
export interface SettingRow {
  key: string;
  group: string;
  type: SettingType;
  env_var?: string;
  default: SettingValue;
  min?: number;
  min_exclusive?: boolean;
  max?: number;
  /** The hard ceiling: going above it needs a code change. */
  ceiling?: number;
  /** Decimals allowed. */
  scale?: number;
  enum?: string[];
  approval: ApprovalRule;
  safe_value?: SettingValue;
  label: string;
  impact: string;
  /** null until the bot has published its values. */
  effective: EffectiveSetting | null;
  stored: StoredSetting | null;
  open_request: OpenSettingRequest | null;
  last_change: SettingChange | null;
}

export interface BotHeartbeat {
  started_at: Timestamp;
  heartbeat_at: Timestamp;
  commit?: string;
  commit_modified?: boolean;
  settings_loaded_at: Timestamp;
  settings_source: 'db' | 'cache' | 'none';
  settings_count: number;
  /** The heartbeat is older than 3 minutes: the bot is probably not running. */
  is_stale: boolean;
}

export interface ProfileStatus {
  pushed_at: Timestamp | null;
  methods: string[] | null;
  error?: string;
  pending_at?: Timestamp | null;
  attempts: number;
}

/** Which secrets the bot has; never their values. */
export interface SecretsStatus {
  tg_bot_token: boolean;
  admin_user_id: boolean;
  apify_token: boolean;
  hiker_token: boolean;
  proxies: number;
  relay_url: boolean;
  relay_token_file: boolean;
}

export interface SettingsPlatform {
  id: string;
  label: string;
}

export interface SettingsResponse extends ResponseMeta {
  bot: BotHeartbeat | null;
  profile: ProfileStatus | null;
  secrets: SecretsStatus | null;
  groups: string[];
  platforms: SettingsPlatform[];
  /** Whether the bot applies queued changes; until then they expire. */
  applying: boolean;
  /** Registry order; empty until the bot has published its registry. */
  settings: SettingRow[];
}

export type SettingRequestKind = 'setting' | 'special_limit';

export interface SettingRequest {
  id: number;
  /** setting: a registry key; special_limit: user:<id> or chat:<id>. */
  kind: SettingRequestKind;
  key: string;
  status: SettingRequestStatus;
  value: SettingValue | null;
  /** A limit request's expected is null when no limit was stored. */
  expected: SettingValue | null;
  needs_approval: boolean | null;
  /** Why the bot rejected or expired it. */
  error: string | null;
  created_at: Timestamp;
  expires_at: Timestamp;
  decided_at: Timestamp | null;
}

export interface SettingRequestResponse extends ResponseMeta {
  request: SettingRequest;
}

export interface SettingChangeDraft {
  key: string;
  /** null: reset to .env / default. */
  value: SettingValue | null;
  /** The effective value the page showed: the bot applies the change only while it still holds. */
  expected: SettingValue;
}

export interface AuditEntry {
  id: number;
  key: string;
  old_value: SettingValue | null;
  new_value: SettingValue | null;
  actor: SettingActor;
  /** The database role that wrote it, which the writer can not forge. */
  db_role: string;
  request_id: number | null;
  note: string | null;
  created_at: Timestamp;
}

export interface AuditResponse extends ResponseMeta {
  entries: AuditEntry[];
  /** Pass as before for the next, older page; null on the last page. */
  next_before: number | null;
}

export interface AuditFilter {
  key?: string;
  group?: string;
  before?: number;
  limit?: number;
}

export const AUDIT_LIMIT = 50;

const SETTINGS_PATH = '/api/settings';
const SETTINGS_REQUESTS_PATH = `${SETTINGS_PATH}/requests`;

export function getSettings(signal?: AbortSignal): Promise<SettingsResponse> {
  return request<SettingsResponse>('GET', SETTINGS_PATH, undefined, signal);
}

/** Queues a change for the bot (202); 409 while another change of the key is waiting. */
export function queueSettingChange(draft: SettingChangeDraft): Promise<SettingRequestResponse> {
  return request<SettingRequestResponse>('POST', SETTINGS_REQUESTS_PATH, draft);
}

export function getSettingRequest(id: number, signal?: AbortSignal): Promise<SettingRequestResponse> {
  return request<SettingRequestResponse>('GET', `${SETTINGS_REQUESTS_PATH}/${id}`, undefined, signal);
}

export function getSettingsAudit(filter: AuditFilter, signal?: AbortSignal): Promise<AuditResponse> {
  const query: PathQuery = { limit: filter.limit ?? AUDIT_LIMIT };

  if (filter.key !== undefined) {
    query.key = filter.key;
  }

  if (filter.group !== undefined) {
    query.group = filter.group;
  }

  if (filter.before !== undefined) {
    query.before = filter.before;
  }

  return request<AuditResponse>('GET', buildPath('settings/audit', query), undefined, signal);
}

// Limits: per-person and per-group special downloads this month, the paid services and the home
// relay. Limit changes are queued like setting changes; the bot applies them without approval.

export type LimitScope = 'user' | 'chat';

/** full, near and ok count against a limit; off: a group switched off or at 0; blocked: a person at 0. */
export type LimitState = 'ok' | 'near' | 'full' | 'blocked' | 'off' | 'unlimited';

/** custom: stored for this person or group; unlimited: -1. */
export type LimitSource = 'default' | 'custom' | 'unlimited';

export interface SpecialLimitRules {
  kind: string;
  min: number;
  max: number;
  /** The bot also takes -1 (unlimited); false or missing on an older bot. */
  unlimited?: boolean;
}

export interface LimitDefaults {
  /** null until the bot has published its settings. */
  user: number | null;
  chat: number | null;
}

export interface GlobalUsage {
  used: number;
  /** null: no bot-wide cap. */
  cap: number | null;
  off: boolean;
}

export interface LimitChange {
  at: Timestamp;
  actor: SettingActor;
  /** null: the default; -1: unlimited. */
  old_value: number | null;
  new_value: number | null;
}

export interface LimitRow {
  scope: LimitScope;
  scope_id: number;
  title: string;
  used: number;
  used_last_month: number;
  /** The stored monthly_limit (null: none); sent back as expected. */
  stored_limit: number | null;
  limit_source: LimitSource;
  /** The limit the bot applies: -1 unlimited; null while the default is unknown. */
  effective_limit: number | null;
  remaining: number | null;
  /** A group switched off with /gated off. */
  enabled: boolean;
  state: LimitState;
  credits: number;
  /** Groups: Stars boosts that were not refunded. */
  boosts: number;
  open_request: SettingRequest | null;
  last_change: LimitChange | null;
}

/** A bot_runtime_status component as published, stale when older than 3 minutes. */
export interface PublishedComponent<T> {
  data: T;
  updated_at: Timestamp;
  is_stale: boolean;
}

export type PaidState = 'on' | 'paused' | 'off' | 'unknown';

export type PaidReason =
  | 'no_token'
  | 'not_listed'
  | 'budget_zero'
  | 'budget_used'
  | 'usage_unreadable'
  | 'usage_not_read'
  | 'balance_empty'
  | 'ledger_error'
  | 'no_database';

export type PaidFeature = 'posts' | 'stories' | 'shorts';

export interface ApifyService {
  has_token: boolean;
  is_listed: boolean;
  budget_usd: number;
  used_usd: number | null;
  in_flight_usd: number;
  usage_read_at: Timestamp | null;
  usage_error?: string;
  usage_error_at: Timestamp | null;
  account_limit_usd: number | null;
  cycle_start: Timestamp | null;
  cycle_end: Timestamp | null;
  state: PaidState;
  reason?: PaidReason;
  paused_features: PaidFeature[] | null;
}

export interface HikerService {
  has_token: boolean;
  is_listed: boolean;
  budget_usd: number;
  price_per_request_usd: number;
  spent_usd: number | null;
  in_flight_usd: number;
  calls: number;
  charged_calls: number;
  gated_calls: number;
  story_calls: number;
  balance_requests: number | null;
  balance_read_at: Timestamp | null;
  exhausted: boolean;
  state: PaidState;
  reason?: PaidReason;
}

export interface PaidServicesData {
  computed_at: Timestamp;
  /** The gated-post provider order. */
  order: string[];
  apify: ApifyService;
  hiker: HikerService;
}

export interface RelayCounters {
  hour: number;
  day: number;
  hourly_limit: number;
  daily_limit: number;
}

export interface RelayPlatform extends RelayCounters {
  enabled: boolean;
  breaker: 'closed' | 'open';
  breaker_left_s: number;
}

export type RelayWindow = '24h' | '7d' | '30d';

export interface RelayHistoryRow {
  platform: string;
  /** OK, a relay error code, or TRANSPORT / REJECTED / SKIPPED for the bot-side outcomes. */
  code: string;
  '24h': number;
  '7d': number;
  '30d': number;
}

export interface RelayHistory {
  windows: RelayWindow[];
  rows: RelayHistoryRow[];
  ok_bytes: { '24h': number; '7d': number };
  ok_avg_ms: { '24h': number | null; '7d': number | null };
}

export interface RelayStatus {
  configured: boolean;
  platforms_in_use: string[] | null;
  checked_at: Timestamp | null;
  reachable: boolean;
  error: string;
  yt_dlp: string;
  breaker_open: string[] | null;
  global: RelayCounters;
  platforms: Record<string, RelayPlatform> | null;
  /** null without a database. */
  history: RelayHistory | null;
}

export interface LimitsResponse extends ResponseMeta {
  /** "2026-10": the UTC month the counters run in. */
  period: string;
  resets_at: Timestamp;
  bot: BotHeartbeat | null;
  /** The bot runs and applies queued changes. */
  applying: boolean;
  /** null until a bot that takes limit changes has published its rules. */
  rules: SpecialLimitRules | null;
  defaults: LimitDefaults;
  global: GlobalUsage;
  paid: PublishedComponent<PaidServicesData> | null;
  relay: PublishedComponent<RelayStatus> | null;
  /** Nearest the limit first. */
  rows: LimitRow[];
  total: number;
  /** More people and groups than rows: find the others by id. */
  truncated: boolean;
}

export interface LimitScopeResponse extends ResponseMeta {
  row: LimitRow;
  defaults: LimitDefaults;
  rules: SpecialLimitRules | null;
  applying: boolean;
}

export interface LimitDraft {
  scope: LimitScope;
  id: number;
  /** null: back to the default. */
  limit: number | null;
  /** The stored limit the page showed. */
  expected: number | null;
}

const LIMITS_PATH = '/api/limits';

export function getLimits(signal?: AbortSignal): Promise<LimitsResponse> {
  return request<LimitsResponse>('GET', LIMITS_PATH, undefined, signal);
}

export function getLimitScope(scope: LimitScope, id: number, signal?: AbortSignal): Promise<LimitScopeResponse> {
  const path = buildPath('limits/scope', { scope, id });

  return request<LimitScopeResponse>('GET', path, undefined, signal);
}

/** Queues a limit change for the bot (202); 409 while one is waiting or before the bot supports it. */
export function queueLimitChange(draft: LimitDraft): Promise<SettingRequestResponse> {
  return request<SettingRequestResponse>('POST', `${LIMITS_PATH}/requests`, draft);
}
