// Pure helpers for the Limits tab (type-only imports, so `node --test` runs them as is). The bot
// validates every limit change again and decides; these give instant feedback and the texts.
import type {
  ApifyService,
  GlobalUsage,
  HikerService,
  LimitChange,
  LimitDefaults,
  LimitRow,
  LimitScope,
  LimitState,
  PaidFeature,
  PublishedComponent,
  RelayHistory,
  RelayPlatform,
  RelayStatus,
  RelayWindow,
  SpecialLimitRules,
} from './api';

/** The quick raises of a row: +10 and +50 on its current limit. */
export const QUICK_STEPS = [10, 50] as const;

/** The Limits tab reloads every 30 s while it is visible. */
export const LIMITS_REFRESH_MS = 30_000;

/** A relay check older than this is stale (the bot checks every minute). */
export const RELAY_STALE_MS = 3 * 60_000;

const UNLIMITED = -1;
const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;
const DAY_MS = 86_400_000;
const HOURS_SHOWN = 48;
const SECOND_MS = 1_000;
const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];
const BYTE_STEP = 1024;
const WHOLE_NUMBER = /^\d+$/;
const OK_CODE = 'OK';
const LOCALE = 'en-GB';

export const RAISE_QUESTION = 'Raise the monthly special downloads of {name} from {old} to {new}?';
export const LOWER_QUESTION = 'Lower the monthly special downloads of {name} from {old} to {new}?';
export const SET_QUESTION = 'Set the monthly special downloads of {name} to {new}? It is {old} now.';
export const RESET_QUESTION = 'Set {name} back to the default ({default} a month)?';
export const APPLY_NOTE = 'Applies within a few seconds, without approval in Telegram. It is logged and listed in your Telegram change digest.';
export const BELOW_USED_WARNING = 'They used {used} this month already, so they get no more special downloads until {resets} (bonus credits still apply).';
export const BLOCK_USER_WARNING = 'A limit of 0 blocks this person: their bonus credits can’t be used either.';
export const GROUP_OFF_WARNING = 'A limit of 0 turns special downloads off in this group.';
export const GROUP_DISABLED_NOTE = 'Special downloads are switched off in this group (/gated off). A new limit applies once it is switched on in Telegram.';
export const UNLIMITED_NOTE = 'Unlimited, set with /gated. Change it in Telegram.';
export const aboveCeilingNote = (max: number): string => `Set above ${max} with /gated; here it can only be lowered.`;
export const ceilingReachedNote = (max: number): string => `At the ${max}-a-month ceiling.`;
export const WAITING_TEXT = 'A change is waiting for the bot.';
export const LIMIT_CONFLICT_TEXT = 'A change of this limit is already waiting. Wait for it to finish, then try again.';
export const NOT_SUPPORTED_TEXT = 'The running bot does not take limit changes yet. Deploy the bot first.';
export const STALE_BOT_TEXT = 'The bot is not running or not applying changes, so a change would expire unapplied.';
export const WRITE_GUARD_TEXT = 'Refused by the dashboard’s write guard; reload the page.';
export const TRUNCATED_TEXT = 'Showing the {shown} busiest of {total}. Use “Find by id” for anyone not listed.';
export const ID_SIGN_HINT = 'Group ids are negative, e.g. -1001234567890; person ids are positive.';
export const NOT_SUPPORTED_SERVER_TEXT = 'deploy the bot first';

const STATE_LABELS: Record<LimitState, string> = {
  full: 'Full',
  near: 'Near the limit',
  ok: 'OK',
  blocked: 'Blocked',
  off: 'Off',
  unlimited: 'Unlimited',
};

const FEATURE_LABELS: Record<PaidFeature, string> = {
  posts: 'Restricted posts',
  stories: 'Stories',
  shorts: 'Shorts',
};

const usdFormat = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const priceFormat = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 6 });
const integerFormat = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 0 });
const oneDecimalFormat = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 1 });
const cycleFormat = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', timeZone: 'UTC' });
const resetFormat = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' });

/** Fills {name}-style placeholders. */
export function fill(template: string, values: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match, name: string) => String(values[name] ?? match));
}

/** "$4.12". */
export function formatUsd(value: number): string {
  return `$${usdFormat.format(value)}`;
}

/** "$0.002": a per-call price with the decimals it has. */
export function formatPrice(value: number): string {
  return `$${priceFormat.format(value)}`;
}

function formatInteger(value: number): string {
  return integerFormat.format(value);
}

/** "1 November 2026" for the first moment of the next UTC month. */
export function formatResetDate(timestamp: string): string {
  const date = new Date(timestamp);

  return resetFormat.format(date);
}

/** The raises +10 / +50 offers: above the current limit, capped at the max, each once. */
export function quickTargets(row: LimitRow, rules: SpecialLimitRules): number[] {
  const base = row.effective_limit ?? 0;
  const isUnlimited = (row.limit_source === 'unlimited');
  const targets = QUICK_STEPS.map((step) => Math.min(base + step, rules.max));
  const unique = [...new Set(targets)].filter((target) => (target > base));

  return (isUnlimited || (base >= rules.max)) ? [] : unique;
}

/** Why a row's actions are off, or null when it can be changed. */
export function canChange(row: LimitRow, page: { applying: boolean; rules: SpecialLimitRules | null }): string | null {
  const checks: [boolean, string][] = [
    [(page.applying === false), STALE_BOT_TEXT],
    [(page.rules === null), NOT_SUPPORTED_TEXT],
    [(row.limit_source === 'unlimited'), UNLIMITED_NOTE],
    [(row.open_request !== null), WAITING_TEXT],
  ];
  const failed = checks.find(([isFailed]) => isFailed);

  return (failed === undefined) ? null : failed[1];
}

/** The use against the limit in words: "12 of 20 (default)", "12 · unlimited", "Off". */
export function limitText(row: LimitRow): string {
  const used = formatInteger(row.used);
  const limit = row.effective_limit;
  const isDefault = (row.limit_source === 'default');
  const ofText = (limit === null) ? `${used} of ?` : `${used} of ${formatInteger(limit)}${isDefault ? ' (default)' : ''}`;
  const isGroupZero = (row.scope === 'chat') && (limit === 0);
  const texts: Record<LimitState, string> = {
    ok: ofText,
    near: ofText,
    full: ofText,
    unlimited: `${used} · unlimited`,
    blocked: 'Blocked (limit 0)',
    off: (isGroupZero && row.enabled) ? 'Off (limit 0)' : 'Off',
  };

  return texts[row.state];
}

export function stateLabel(state: LimitState): string {
  return STATE_LABELS[state];
}

/** The note under a row's actions: why it can only be lowered, or that it is at the ceiling. */
export function ceilingNote(row: LimitRow, rules: SpecialLimitRules | null): string | null {
  const limit = row.effective_limit;
  const max = rules?.max ?? null;
  const isCustom = (row.limit_source === 'custom');
  let result: string | null = null;

  if ((max !== null) && (limit !== null) && (limit === max)) {
    result = ceilingReachedNote(max);
  }

  if ((max !== null) && isCustom && (limit !== null) && (limit > max)) {
    result = aboveCeilingNote(max);
  }

  return result;
}

/** A row's name: the group title or @username, else its kind and id. */
export function rowName(row: { scope: LimitScope; scope_id: number; title: string }): string {
  const fallback = (row.scope === 'chat') ? `group ${row.scope_id}` : `person ${row.scope_id}`;
  const named = (row.scope === 'chat') ? row.title : `@${row.title}`;

  return (row.title === '') ? fallback : named;
}

/** One key per person or group, so React gives each its own row state (confirm, field, followed request). */
export function limitTargetKey(scope: LimitScope, id: number): string {
  return `${scope}:${id}`;
}

/** A stored limit in words: null the default, -1 unlimited. */
export function storedLimitText(limit: number | null, defaultLimit: number | null): string {
  const defaultText = (defaultLimit === null) ? 'the default' : `the default (${formatInteger(defaultLimit)})`;
  let result = defaultText;

  if (limit === UNLIMITED) {
    result = 'unlimited';
  }

  if ((limit !== null) && (limit !== UNLIMITED)) {
    result = formatInteger(limit);
  }

  return result;
}

export interface LimitConfirm {
  question: string;
  warning: string | null;
  danger: boolean;
  action: string;
}

/** What a change to next (null: back to the default) does, for the confirmation. */
export function limitConfirm(row: LimitRow, next: number | null, defaults: LimitDefaults, resets: string = 'the 1st of next month'): LimitConfirm {
  const defaultLimit = (row.scope === 'chat') ? defaults.chat : defaults.user;
  const target = next ?? defaultLimit;
  const current = row.effective_limit ?? 0;
  const name = rowName(row);
  const values = { name, old: storedLimitText(row.stored_limit, defaultLimit), new: formatInteger(next ?? 0), default: (defaultLimit === null) ? '?' : formatInteger(defaultLimit) };
  const isZero = (target === 0);
  const isBelowUsed = (target !== null) && (target > 0) && (target <= row.used) && (row.used > 0);
  const isChat = (row.scope === 'chat');
  let result: LimitConfirm = { question: fill(SET_QUESTION, values), warning: null, danger: false, action: `Set to ${values.new}` };

  if ((next !== null) && (next > current)) {
    result = { ...result, question: fill(RAISE_QUESTION, values), action: `Raise to ${values.new}` };
  }

  if ((next !== null) && (next < current)) {
    result = { ...result, question: fill(LOWER_QUESTION, values), action: `Lower to ${values.new}` };
  }

  if (next === null) {
    result = { ...result, question: fill(RESET_QUESTION, values), action: 'Reset to default' };
  }

  if (isBelowUsed) {
    result = { ...result, warning: fill(BELOW_USED_WARNING, { used: formatInteger(row.used), resets }), danger: true };
  }

  if (isZero && isChat) {
    result = { ...result, warning: GROUP_OFF_WARNING, danger: true, action: (next === null) ? result.action : 'Turn off' };
  }

  if (isZero && (isChat === false)) {
    result = { ...result, warning: BLOCK_USER_WARNING, danger: true, action: (next === null) ? result.action : 'Block' };
  }

  return result;
}

/** Reads the Set field: a whole number within the rules. */
export function parseLimitInput(text: string, rules: SpecialLimitRules): { value: number | null; error: string | null } {
  const trimmed = text.trim();
  const value = WHOLE_NUMBER.test(trimmed) ? Number(trimmed) : null;
  const isInRange = (value !== null) && (value >= rules.min) && (value <= rules.max);
  const error = isInRange ? null : `must be a whole number from ${rules.min} to ${rules.max}`;

  return { value: isInRange ? value : null, error };
}

/** The list filter: All / People / Groups and a search on the name, @username or id. */
export function matchesFilter(row: LimitRow, scope: 'all' | LimitScope, query: string): boolean {
  const isScope = (scope === 'all') || (row.scope === scope);
  const needle = query.trim().toLowerCase().replace(/^@/, '');
  const haystack = `${row.title.toLowerCase()} ${row.scope_id}`;
  const isMatch = (needle === '') || haystack.includes(needle);

  return isScope && isMatch;
}

/** An audit key of a limit as the owner reads it; null for any other key. */
export function specialLimitLabel(key: string): string | null {
  const match = /^special_(limit|enabled):(user|chat):(-?\d+)$/.exec(key);
  const subjects: Record<string, string> = { user: 'person', chat: 'group' };
  const kinds: Record<string, string> = { limit: 'Special-downloads limit', enabled: 'Special downloads on/off' };

  return (match === null) ? null : `${kinds[match[1] as string]} · ${subjects[match[2] as string]} ${match[3]}`;
}

/** An audit value of a limit key: null the default, -1 unlimited, true / false on / off. */
export function specialValueText(value: unknown): string {
  const texts: [boolean, string][] = [
    [(value === null), 'default'],
    [(value === UNLIMITED), 'unlimited'],
    [(value === true), 'On'],
    [(value === false), 'Off'],
  ];
  const found = texts.find(([isFound]) => isFound);

  return (found === undefined) ? String(value) : found[1];
}

/** How a paid service stands, in one line. */
export function paidStateText(service: ApifyService | HikerService): string {
  const paused = ('paused_features' in service) ? (service.paused_features ?? []) : [];
  const pausedText = paused.map((feature) => FEATURE_LABELS[feature]).join(', ');
  const byReason: Record<string, string> = {
    'paused/budget_used': 'Paused: monthly budget used up',
    'paused/budget_zero': 'Paused: budget is $0',
    'paused/usage_unreadable': 'Paused: Apify usage can’t be read, runs are refused until it can',
    'unknown/usage_not_read': 'Usage not read yet (checked before each run)',
    'paused/balance_empty': 'Paused: HikerAPI balance is empty (rechecked hourly)',
    'unknown/ledger_error': 'Spend unknown: the ledger could not be read',
    'paused/no_database': 'Paused: HikerAPI needs the database for its budget',
    'off/no_token': 'Off: no token in .env',
    'off/not_listed': 'Not used: not in the provider order',
  };
  const running = (paused.length === 0) ? 'Running' : `Running · ${pausedText} paused`;
  const text = byReason[`${service.state}/${service.reason ?? ''}`];

  return (service.state === 'on') ? running : (text ?? service.state);
}

/** The badge tone of a paid service. */
export function paidTone(service: ApifyService | HikerService): 'ok' | 'warning' | 'danger' | 'muted' {
  const tones: Record<string, 'ok' | 'warning' | 'danger' | 'muted'> = { on: 'ok', paused: 'danger', unknown: 'warning', off: 'muted' };
  const paused = ('paused_features' in service) ? (service.paused_features ?? []) : [];
  const tone = tones[service.state] ?? 'muted';

  return ((service.state === 'on') && (paused.length > 0)) ? 'warning' : tone;
}

/** One line per paused Apify feature: "Shorts paused: only $0.08 left". */
export function pausedFeatureLines(service: ApifyService): string[] {
  const left = Math.max(0, service.budget_usd - (service.used_usd ?? 0) - service.in_flight_usd);
  const paused = service.paused_features ?? [];

  return paused.map((feature) => `${FEATURE_LABELS[feature]} paused: only ${formatUsd(left)} left, less than one run may cost`);
}

/** The Apify cycle, e.g. "1 Oct – 31 Oct"; null when the bot has not read it. */
export function cycleText(service: ApifyService): string | null {
  const start = (service.cycle_start === null) ? null : new Date(service.cycle_start);
  const end = (service.cycle_end === null) ? null : new Date(service.cycle_end);
  const isKnown = (start !== null) && (end !== null);

  return isKnown ? `${cycleFormat.format(start)} – ${cycleFormat.format(end)}` : null;
}

/** "$4.12 of $10.00 · Apify billing cycle (1 Oct – 31 Oct)". */
export function apifySpendText(service: ApifyService): string {
  const cycle = cycleText(service);
  const used = (service.used_usd === null) ? '?' : formatUsd(service.used_usd);
  const cycleNote = (cycle === null) ? 'Apify billing cycle' : `Apify billing cycle (${cycle})`;

  return `${used} of ${formatUsd(service.budget_usd)} · ${cycleNote}`;
}

/** "$1.23 of $5.00 this month (UTC) · 617 billed calls (500 posts, 117 stories) · $0.002 a call". */
export function hikerSpendText(service: HikerService): string {
  const spent = (service.spent_usd === null) ? '?' : formatUsd(service.spent_usd);
  const calls = `${formatInteger(service.charged_calls)} billed calls (${formatInteger(service.gated_calls)} posts, ${formatInteger(service.story_calls)} stories)`;

  return `${spent} of ${formatUsd(service.budget_usd)} this month (UTC) · ${calls} · ${formatPrice(service.price_per_request_usd)} a call`;
}

/** The Apify account limit, shown only when it is below the budget. */
export function accountLimitText(service: ApifyService): string | null {
  const limit = service.account_limit_usd;
  const isBelow = (limit !== null) && (limit < service.budget_usd);

  return isBelow ? `Apify account limit: ${formatUsd(limit as number)} (below the budget)` : null;
}

/** How long ago something was read: "read 12 min ago". */
export function usageAge(at: string | null, now: number): string {
  const elapsed = (at === null) ? 0 : Math.max(0, now - Date.parse(at));
  const units: [boolean, string][] = [
    [(at === null), 'not read yet'],
    [(elapsed < MINUTE_MS), 'read just now'],
    [(elapsed < HOUR_MS), `read ${Math.round(elapsed / MINUTE_MS)} min ago`],
    [(elapsed < (HOURS_SHOWN * HOUR_MS)), `read ${Math.round(elapsed / HOUR_MS)} h ago`],
  ];
  const found = units.find(([isFound]) => isFound);

  return (found === undefined) ? `read ${Math.round(elapsed / DAY_MS)} days ago` : found[1];
}

/** Minutes since a timestamp, rounded. */
export function minutesSince(at: string, now: number): number {
  const elapsed = now - Date.parse(at);
  const minutes = Math.round(elapsed / MINUTE_MS);

  return Math.max(0, minutes);
}

export type RelayCardKind = 'waiting' | 'not_configured' | 'stale' | 'reachable' | 'unreachable';

export interface RelayCardState {
  kind: RelayCardKind;
  label: string;
  tone: 'ok' | 'warning' | 'danger' | 'muted';
}

/** The relay card's badge: waiting for the bot, not configured, stale, reachable or not. */
export function relayCardState(relay: PublishedComponent<RelayStatus> | null, isBotStale: boolean, now: number): RelayCardState {
  const data = relay?.data ?? null;
  const checkedAt = data?.checked_at ?? null;
  const isOld = (checkedAt === null) || ((now - Date.parse(checkedAt)) > RELAY_STALE_MS);
  const isStale = isBotStale || (relay?.is_stale === true) || isOld;
  const openBreakers = data?.breaker_open ?? [];
  const reachableTone = (openBreakers.length > 0) ? 'warning' : 'ok';
  const reachableLabel = (openBreakers.length > 0) ? `Reachable · breaker open: ${openBreakers.join(', ')}` : 'Reachable';
  const states: [boolean, RelayCardState][] = [
    [(data === null), { kind: 'waiting', label: 'Waiting for the bot', tone: 'muted' }],
    [(data?.configured === false), { kind: 'not_configured', label: 'Not configured', tone: 'muted' }],
    [isStale, { kind: 'stale', label: 'Stale: no recent check', tone: 'warning' }],
    [(data?.reachable === true), { kind: 'reachable', label: reachableLabel, tone: reachableTone }],
  ];
  const found = states.find(([isFound]) => isFound);

  return (found === undefined) ? { kind: 'unreachable', label: 'Unreachable', tone: 'danger' } : found[1];
}

/** A platform's breaker in words: "closed", "open, 4 min left". */
export function breakerText(platform: RelayPlatform): string {
  const minutes = Math.ceil(platform.breaker_left_s / 60);
  const isOpen = (platform.breaker === 'open');

  return isOpen ? `open, ${minutes} min left` : 'closed';
}

export interface RelayFailure {
  code: string;
  count: number;
}

export interface RelayWindowStats {
  ok: number;
  failed: number;
  /** ok / (ok + failed); null without calls. */
  rate: number | null;
  failures: RelayFailure[];
}

export interface RelayPlatformHistory {
  platform: string;
  windows: Record<RelayWindow, RelayWindowStats>;
}

function windowStats(rows: RelayHistory['rows'], window: RelayWindow): RelayWindowStats {
  const ok = rows.filter((row) => (row.code === OK_CODE)).reduce((total, row) => total + row[window], 0);
  const failures = rows.filter((row) => (row.code !== OK_CODE) && (row[window] > 0)).map((row) => ({ code: row.code, count: row[window] }));
  const failed = failures.reduce((total, failure) => total + failure.count, 0);
  const calls = ok + failed;

  return { ok, failed, rate: (calls === 0) ? null : (ok / calls), failures };
}

/** The history by platform: OK and failures by code for each window, with the success rate. */
export function relayHistoryByPlatform(history: RelayHistory): RelayPlatformHistory[] {
  const names = history.rows.map((row) => row.platform);
  const platforms = [...new Set(names)].sort();
  const windows: RelayWindow[] = ['24h', '7d', '30d'];

  return platforms.map((platform) => {
    const rows = history.rows.filter((row) => (row.platform === platform));
    const entries = windows.map((window) => [window, windowStats(rows, window)] as const);

    return { platform, windows: Object.fromEntries(entries) as Record<RelayWindow, RelayWindowStats> };
  });
}

/** "1.2 GB". */
export function formatBytes(bytes: number): string {
  let value = bytes;
  let unit = 0;

  while ((value >= BYTE_STEP) && (unit < (BYTE_UNITS.length - 1))) {
    value /= BYTE_STEP;
    unit += 1;
  }

  return `${oneDecimalFormat.format(value)} ${BYTE_UNITS[unit]}`;
}

/** "8.4 s"; "—" without a value. */
export function formatDuration(ms: number | null): string {
  return (ms === null) ? '—' : `${oneDecimalFormat.format(ms / SECOND_MS)} s`;
}

/** A success rate: "92%"; "—" without calls. */
export function formatRate(rate: number | null): string {
  return (rate === null) ? '—' : `${Math.round(rate * 100)}%`;
}

const HTTP_FORBIDDEN = 403;
const HTTP_CONFLICT = 409;

/** A refused limit change as one sentence for its row. */
export function limitErrorText(status: number, message: string, isApplying: boolean): string {
  const isNotSupported = (status === HTTP_CONFLICT) && message.includes(NOT_SUPPORTED_SERVER_TEXT);
  const conflict = isApplying ? LIMIT_CONFLICT_TEXT : `${LIMIT_CONFLICT_TEXT} ${STALE_BOT_TEXT}`;
  const texts: [boolean, string][] = [
    [isNotSupported, NOT_SUPPORTED_TEXT],
    [(status === HTTP_CONFLICT), conflict],
    [(status === HTTP_FORBIDDEN), WRITE_GUARD_TEXT],
  ];
  const found = texts.find(([isFound]) => isFound);

  return (found === undefined) ? `Not sent: ${message}.` : found[1];
}

/** Reads a typed id for find by id: a whole number with the scope's sign. */
export function parseScopeId(text: string, scope: LimitScope): { id: number | null; error: string | null } {
  const trimmed = text.trim();
  const isNumber = /^-?\d+$/.test(trimmed);
  const value = isNumber ? Number(trimmed) : Number.NaN;
  const isSafe = Number.isSafeInteger(value);
  const isSigned = (scope === 'chat') ? (value < 0) : (value > 0);
  const isValid = isSafe && isSigned;
  const error = (scope === 'chat') ? 'A group id is a negative whole number, e.g. -1001234567890.' : 'A person id is a positive whole number.';

  return { id: isValid ? value : null, error: isValid ? null : error };
}

/** The note of the bot-wide tile: "of 400", "no cap" or "off". */
export function globalCapNote(global: GlobalUsage): string {
  const capText = (global.cap === null) ? 'no cap' : `of ${formatInteger(global.cap)}`;

  return global.off ? 'off' : capText;
}

/** A limit as a number to compare: the default for null, infinity for unlimited, null when unknown. */
function comparableLimit(limit: number | null, defaultLimit: number | null): number | null {
  const value = limit ?? defaultLimit;

  return (value === UNLIMITED) ? Number.POSITIVE_INFINITY : value;
}

/** "Raised the default (20) → 30 by the dashboard, 18 Oct, 12:04". */
export function limitChangeText(change: LimitChange, defaultLimit: number | null, who: string, when: string): string {
  const oldValue = comparableLimit(change.old_value, defaultLimit);
  const newValue = comparableLimit(change.new_value, defaultLimit);
  const isKnown = (oldValue !== null) && (newValue !== null);
  const verbs: [boolean, string][] = [
    [(isKnown && ((newValue as number) > (oldValue as number))), 'Raised'],
    [(isKnown && ((newValue as number) < (oldValue as number))), 'Lowered'],
  ];
  const found = verbs.find(([isFound]) => isFound);
  const verb = (found === undefined) ? 'Changed' : found[1];
  const oldText = storedLimitText(change.old_value, defaultLimit);
  const newText = storedLimitText(change.new_value, defaultLimit);

  return `${verb} ${oldText} → ${newText} by ${who}, ${when}`;
}
