// Pure helpers for the External services panel (type-only imports, so `node --test` runs them as is).
// The bot computes every state; these only group, order and word what it published.
import type { ServiceErrorCode, ServiceKind, ServiceOffReason, ServiceRow, ServiceSignal, ServiceSignalState, ServiceState } from './api';
import type { Tone } from './components/Bits';

/** A probe runs hourly; one with no result for 2 h is overdue. */
export const PROBE_OVERDUE_MS = 2 * 3_600_000;
/** The group of the rows used by several platforms (the proxy pool). */
export const SHARED_GROUP = 'shared';
export const SHARED_GROUP_LABEL = 'Shared';
export const NO_SIGNAL_TEXT = '—';
export const FALLBACK_NOTE = 'Fallback rows fill only when the main step fails. The main downloader (yt-dlp) is reflected in Platform health above.';
export const SERVICES_EMPTY_TEXT = 'The bot hasn’t published service health yet (deploy the bot first).';
const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;
const DAY_MS = 86_400_000;
const HOURS_SHOWN = 48;
const LOCALE = 'en-GB';

export const SERVICE_KIND_LABELS: Record<ServiceKind, string> = {
  free: 'Free mirror',
  paid: 'Paid API',
  relay: 'Home relay',
  proxy: 'Proxies',
};

export const OFF_REASON_TEXT: Record<ServiceOffReason, string> = {
  platform_off: 'platform switched off',
  not_configured: 'not configured',
  not_listed: 'not in the provider order',
  proxies_off: 'proxies off',
  special_off: 'special downloads off',
  relay_disabled: 'disabled on the relay',
  budget_paused: 'budget',
};

/** budget_paused reads "Paused: budget", every other reason "Off: …". */
const OFF_PREFIX: Partial<Record<ServiceOffReason, string>> = { budget_paused: 'Paused' };
const DEFAULT_OFF_PREFIX = 'Off';

export const ERROR_CODE_TEXT: Record<ServiceErrorCode, string> = {
  timeout: 'Timed out',
  http_4xx: 'Refused (HTTP 4xx)',
  http_5xx: 'Server error (HTTP 5xx)',
  http_429: 'Rate limited (HTTP 429)',
  transport: 'Connection failed',
  parse: 'Unreadable answer',
  wrong_content: 'Wrong content',
  not_configured: 'Not configured',
  other: 'Other error',
};

/** Wording of a state, reason or kind a newer bot published that this dashboard does not know. */
const UNKNOWN_VALUE_PREFIX = 'Unknown';
const UNKNOWN_TONE: Tone = 'muted';

/** Worst first; the bot's catalog order breaks ties; unknown states after all of them. */
const SEVERITY: ServiceState[] = ['down', 'degraded', 'stale', 'ok', 'unknown', 'idle', 'off'];
const FAILING_STATES: (ServiceState | ServiceSignalState)[] = ['down', 'degraded'];

const STATE_TONES: Partial<Record<string, Tone>> = {
  ok: 'ok',
  degraded: 'warning',
  down: 'danger',
  stale: 'muted',
  idle: 'muted',
  unknown: 'muted',
  off: 'muted',
};

const clockFormat = (timeZone: string): Intl.DateTimeFormat => new Intl.DateTimeFormat(LOCALE, {
  day: 'numeric',
  month: 'short',
  hour: '2-digit',
  minute: '2-digit',
  timeZone,
});

export interface ServiceGroup {
  /** A platform id, or SHARED_GROUP. */
  key: string;
  rows: ServiceRow[];
}

export interface ServiceBadge {
  text: string;
  tone: Tone;
}

/** The text of key in texts, or fallback for a value this dashboard does not know. */
function textOr(texts: Partial<Record<string, string>>, key: string, fallback: string): string {
  const result = texts[key] ?? fallback;

  return result;
}

function unknownValueText(value: string): string {
  const result = `${UNKNOWN_VALUE_PREFIX}: ${value}`;

  return result;
}

/** The kind's label ("Free mirror"), or the kind itself when unknown. */
export function serviceKindLabel(kind: ServiceKind): string {
  const result = textOr(SERVICE_KIND_LABELS, kind, kind);

  return result;
}

/** The group key of a row: its platform, or SHARED_GROUP when several use it. */
function groupKeyOf(row: ServiceRow): string {
  const result = (row.platforms.length === 1) ? (row.platforms[0] as string) : SHARED_GROUP;

  return result;
}

/** A group's place: known platforms in order, unknown ones after them, Shared last. */
function groupRank(key: string, order: readonly string[], firstSeen: number): number {
  const known = order.indexOf(key);
  const unknownRank = order.length + firstSeen;
  const sharedRank = Number.MAX_SAFE_INTEGER;
  const rankOfOther = (key === SHARED_GROUP) ? sharedRank : unknownRank;
  const result = (known >= 0) ? known : rankOfOther;

  return result;
}

/** Groups the rows by platform in the given order, each group worst first, then in catalog order. */
export function groupServices(rows: ServiceRow[], order: readonly string[]): ServiceGroup[] {
  const groups = new Map<string, { index: number; rows: { row: ServiceRow; index: number }[] }>();

  rows.forEach((row, index) => {
    const key = groupKeyOf(row);
    const group = groups.get(key) ?? { index: groups.size, rows: [] };
    group.rows.push({ row, index });
    groups.set(key, group);
  });

  const entries = [...groups.entries()];
  const sorted = entries.sort(([left, leftGroup], [right, rightGroup]) => groupRank(left, order, leftGroup.index) - groupRank(right, order, rightGroup.index));
  const result = sorted.map(([key, group]) => ({ key, rows: sortBySeverity(group.rows) }));

  return result;
}

function sortBySeverity(rows: { row: ServiceRow; index: number }[]): ServiceRow[] {
  const sorted = [...rows].sort((left, right) => severityCompare(left, right));
  const result = sorted.map((item) => item.row);

  return result;
}

function severityRank(state: ServiceState): number {
  const index = SEVERITY.indexOf(state);
  const result = (index < 0) ? SEVERITY.length : index;

  return result;
}

function severityCompare(left: { row: ServiceRow; index: number }, right: { row: ServiceRow; index: number }): number {
  const bySeverity = severityRank(left.row.state) - severityRank(right.row.state);
  const result = (bySeverity === 0) ? (left.index - right.index) : bySeverity;

  return result;
}

/** How long ago: "just now", "12 min ago", "5 h ago", "3 days ago"; null gives "—". */
export function ageText(at: string | null, now: number): string {
  const elapsed = (at === null) ? 0 : Math.max(0, now - Date.parse(at));
  const units: [boolean, string][] = [
    [(at === null), NO_SIGNAL_TEXT],
    [(elapsed < MINUTE_MS), 'just now'],
    [(elapsed < HOUR_MS), `${Math.round(elapsed / MINUTE_MS)} min ago`],
    [(elapsed < (HOURS_SHOWN * HOUR_MS)), `${Math.round(elapsed / HOUR_MS)} h ago`],
  ];
  const found = units.find(([isFound]) => isFound);
  const result = (found === undefined) ? `${Math.round(elapsed / DAY_MS)} days ago` : found[1];

  return result;
}

function offBadgeText(reason: ServiceOffReason | null): string {
  const prefix = (reason === null) ? DEFAULT_OFF_PREFIX : (OFF_PREFIX[reason] ?? DEFAULT_OFF_PREFIX);
  const text = (reason === null) ? OFF_REASON_TEXT.not_configured : textOr(OFF_REASON_TEXT, reason, reason);
  const result = `${prefix}: ${text}`;

  return result;
}

/** The row's badge, worded from the state the bot computed. */
export function serviceBadge(row: ServiceRow, now: number): ServiceBadge {
  const lastFailure = row.traffic?.last_failure_at ?? null;
  const texts: Partial<Record<string, () => string>> = {
    ok: () => 'OK',
    degraded: () => 'Degraded',
    down: () => 'Down',
    stale: () => `Last failed ${ageText(lastFailure, now)}`,
    idle: () => 'Not needed recently',
    unknown: () => 'No data yet',
    off: () => offBadgeText(row.off_reason),
  };
  const text = texts[row.state];
  const tone = STATE_TONES[row.state] ?? UNKNOWN_TONE;
  const result = (text === undefined) ? { text: unknownValueText(row.state), tone: UNKNOWN_TONE } : { text: text(), tone };

  return result;
}

function failingText(signal: ServiceSignal, timeZone: string): string {
  const since = signal.failing_since ?? signal.last_failure_at;
  const format = clockFormat(timeZone);
  const sinceText = (since === null) ? '' : ` since ${format.format(new Date(since))}`;
  const result = `Failing, ${signal.consecutive_failures} in a row${sinceText}`;

  return result;
}

function okText(signal: ServiceSignal, now: number): string {
  const result = (signal.last_success_at === null) ? 'OK' : `OK ${ageText(signal.last_success_at, now)}`;

  return result;
}

function trafficSignalText(signal: ServiceSignal, now: number, timeZone: string): string {
  const texts: Partial<Record<string, () => string>> = {
    ok: () => okText(signal, now),
    degraded: () => failingText(signal, timeZone),
    down: () => failingText(signal, timeZone),
    stale: () => `Last failed ${ageText(signal.last_failure_at, now)}`,
  };
  const text = texts[signal.state];
  const result = (text === undefined) ? unknownValueText(signal.state) : text();

  return result;
}

/** The traffic signal in a few words: "OK 5 min ago", "Failing, 3 in a row since …", "Last failed 2 days ago". */
export function trafficText(row: ServiceRow, now: number, timeZone: string): string {
  const traffic = row.traffic;
  let result = NO_SIGNAL_TEXT;

  if ((row.has_traffic === true) && (traffic === null)) {
    result = 'None yet';
  }

  if ((row.has_traffic === true) && (traffic !== null)) {
    result = trafficSignalText(traffic, now, timeZone);
  }

  return result;
}

function newestOf(signal: ServiceSignal): number {
  const times = [signal.last_success_at, signal.last_failure_at].filter((at): at is string => (at !== null));
  const parsed = times.map((at) => Date.parse(at));
  const result = (parsed.length === 0) ? Number.NEGATIVE_INFINITY : Math.max(...parsed);

  return result;
}

function probeSignalText(signal: ServiceSignal, now: number): string {
  const isOk = (signal.state === 'ok');
  const streak = (signal.consecutive_failures > 1) ? ` (${signal.consecutive_failures} in a row)` : '';
  const failedText = `Check failed ${ageText(signal.last_failure_at, now)}${streak}`;
  const text = isOk ? `Check OK ${ageText(signal.last_success_at, now)}` : failedText;
  const isOverdue = ((now - newestOf(signal)) > PROBE_OVERDUE_MS);
  const result = isOverdue ? `${text}, overdue` : text;

  return result;
}

/** The hourly check in a few words: "Check OK 20 min ago", "Check failed 5 min ago", with ", overdue" past 2 h. */
export function probeText(row: ServiceRow, now: number): string {
  const probe = row.probe;
  let result = NO_SIGNAL_TEXT;

  if ((row.has_probe === true) && (probe === null)) {
    result = 'Not run yet';
  }

  if ((row.has_probe === true) && (probe !== null)) {
    result = probeSignalText(probe, now);
  }

  return result;
}

/** The signal that makes the row fail: the one in the row's state, else any failing one. */
function failingSignal(row: ServiceRow): ServiceSignal | null {
  const signals = [row.traffic, row.probe].filter((signal): signal is ServiceSignal => (signal !== null));
  const failing = signals.filter((signal) => FAILING_STATES.includes(signal.state));
  const matching = failing.find((signal) => (signal.state === row.state));
  const result = matching ?? failing[0] ?? null;

  return result;
}

/** Whether the row shows its last error: only while it is degraded or down. */
export function errorShown(row: ServiceRow): boolean {
  const signal = failingSignal(row);
  const isFailing = FAILING_STATES.includes(row.state);
  const result = isFailing && (signal !== null) && (signal.last_error_code !== null);

  return result;
}

/** The last error of a failing row in words; null when errorShown is false. */
export function errorText(row: ServiceRow): string | null {
  const code = failingSignal(row)?.last_error_code ?? null;
  const isShown = errorShown(row) && (code !== null);
  const result = isShown ? textOr(ERROR_CODE_TEXT, code as ServiceErrorCode, ERROR_CODE_TEXT.other) : null;

  return result;
}

/** "All services look fine." or "N of M need a look.", then the fallback note. */
export function servicesSubtitle(rows: ServiceRow[]): string {
  const flagged = rows.filter((row) => FAILING_STATES.includes(row.state)).length;
  const summary = (flagged === 0) ? 'All services look fine.' : `${flagged} of ${rows.length} need a look.`;
  const result = `${summary} ${FALLBACK_NOTE}`;

  return result;
}
