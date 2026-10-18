// Pure formatting and data-shaping helpers. Dates from the API ("YYYY-MM-DD",
// "YYYY-MM") are already in the dashboard timezone, so they are formatted as
// calendar values in UTC; timestamps are formatted in the API's timezone.

const LOCALE = 'en-GB';
const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;
const DAY_MS = 86_400_000;
const LONG_TEXT_CHARS = 240;
const LONG_TEXT_LINES = 3;
/** Shown for a missing timestamp. */
export const NO_TIMESTAMP = '—';

const integerFormat = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 0 });
const decimalFormat = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const compactFormat = new Intl.NumberFormat(LOCALE, { notation: 'compact', maximumFractionDigits: 1 });
const percentFormat = new Intl.NumberFormat(LOCALE, { style: 'percent', maximumFractionDigits: 0 });
const signedPercentFormat = new Intl.NumberFormat(LOCALE, { signDisplay: 'exceptZero', maximumFractionDigits: 0 });
const dayFormat = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', timeZone: 'UTC' });
const fullDayFormat = new Intl.DateTimeFormat(LOCALE, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' });
const monthFormat = new Intl.DateTimeFormat(LOCALE, { month: 'short', timeZone: 'UTC' });
const fullMonthFormat = new Intl.DateTimeFormat(LOCALE, { month: 'long', year: 'numeric', timeZone: 'UTC' });
const relativeFormat = new Intl.RelativeTimeFormat(LOCALE, { numeric: 'auto', style: 'short' });

const PLATFORM_LABELS: Record<string, string> = {
  tiktok: 'TikTok',
  instagram: 'Instagram',
  twitter: 'Twitter/X',
  youtube: 'YouTube',
  reddit: 'Reddit',
  facebook: 'Facebook',
  '9gag': '9GAG',
  threads: 'Threads',
  unknown: 'Unknown',
};

export function formatNumber(value: number): string {
  return integerFormat.format(value);
}

export function formatDecimal(value: number): string {
  return decimalFormat.format(value);
}

/** 1,284 / 12.9K: for stat tiles and axis ticks. */
export function formatCompact(value: number): string {
  const result = (Math.abs(value) < 10_000) ? integerFormat.format(value) : compactFormat.format(value);

  return result;
}

export function formatShare(part: number, whole: number): string {
  const result = (whole > 0) ? percentFormat.format(part / whole) : '–';

  return result;
}

/** -17.3 -> "-17%"; null (no previous average) -> "no baseline". */
export function formatChangePct(value: number | null): string {
  const result = (value === null) ? 'no baseline' : `${signedPercentFormat.format(value)}%`;

  return result;
}

function calendarDate(value: string): Date {
  const isMonth = (value.length === 7);
  const iso = isMonth ? `${value}-01` : value;

  return new Date(`${iso}T00:00:00Z`);
}

/** "2026-07-20" -> "20 Jul". */
export function formatDay(day: string): string {
  return dayFormat.format(calendarDate(day));
}

/** "2026-07-20" -> "Mon, 20 Jul 2026". */
export function formatFullDay(day: string): string {
  return fullDayFormat.format(calendarDate(day));
}

/** Week start -> "Week of 20 Jul". */
export function formatWeek(week: string): string {
  return `Week of ${formatDay(week)}`;
}

/** "2026-10" -> "Oct". */
export function formatMonth(month: string): string {
  return monthFormat.format(calendarDate(month));
}

/** "2026-10" -> "October 2026". */
export function formatFullMonth(month: string): string {
  return fullMonthFormat.format(calendarDate(month));
}

export function formatClock(timestamp: string, timeZone: string): string {
  const format = new Intl.DateTimeFormat(LOCALE, { hour: '2-digit', minute: '2-digit', timeZone });

  return format.format(new Date(timestamp));
}

export function formatTimestamp(timestamp: string | null, timeZone: string): string {
  const format = new Intl.DateTimeFormat(LOCALE, {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    timeZone,
  });
  const result = (timestamp === null) ? NO_TIMESTAMP : format.format(new Date(timestamp));

  return result;
}

/** Relative to the response's generated_at, so it matches the data; null -> "—". */
export function formatRelative(timestamp: string | null, now: string): string {
  const result = (timestamp === null) ? NO_TIMESTAMP : formatElapsed(timestamp, now);

  return result;
}

function formatElapsed(timestamp: string, now: string): string {
  const elapsed = Date.parse(now) - Date.parse(timestamp);
  let result = relativeFormat.format(-Math.round(elapsed / DAY_MS), 'day');

  if (elapsed < MINUTE_MS) {
    result = 'just now';
  }

  if ((elapsed >= MINUTE_MS) && (elapsed < HOUR_MS)) {
    result = relativeFormat.format(-Math.round(elapsed / MINUTE_MS), 'minute');
  }

  if ((elapsed >= HOUR_MS) && (elapsed < (2 * DAY_MS))) {
    result = relativeFormat.format(-Math.round(elapsed / HOUR_MS), 'hour');
  }

  return result;
}

const pluralRules = new Intl.PluralRules(LOCALE);

/** 1 -> "1 group", 3 -> "3 groups". */
export function formatCount(value: number, one: string, many: string): string {
  const word = (pluralRules.select(value) === 'one') ? one : many;

  return `${integerFormat.format(value)} ${word}`;
}

export function platformLabel(platform: string): string {
  return PLATFORM_LABELS[platform] ?? platform;
}

const PLATFORM_SETTING_TYPE = 'platform';
const BOOLEAN_VALUES: Record<string, boolean> = { true: true, false: false };

/**
 * Whether a chat setting is on, read the way the bot reads it: platform rows
 * keep the state in `enabled`, every other type stores a boolean in `value`
 * (and always enabled=true).
 */
export function isSettingOn(setting: { type: string; value: string; enabled: boolean }): boolean {
  const fromValue = BOOLEAN_VALUES[setting.value.trim()];
  const usesValue = (setting.type !== PLATFORM_SETTING_TYPE) && (fromValue !== undefined);
  const result = usesValue ? fromValue : setting.enabled;

  return result;
}

export function chatKind(chatId: number): 'group' | 'private' {
  const result = (chatId < 0) ? 'group' : 'private';

  return result;
}

export function userLabel(username: string, fallbackId: number): string {
  const result = (username === '') ? `User ${fallbackId}` : `@${username}`;

  return result;
}

export function chatLabel(title: string, chatId: number): string {
  const result = (title === '') ? `Chat ${chatId}` : title;

  return result;
}

export function isLongText(text: string): boolean {
  const lines = text.split('\n').length;

  return (text.length > LONG_TEXT_CHARS) || (lines > LONG_TEXT_LINES);
}

export function sumBy<T>(rows: T[], pick: (row: T) => number): number {
  return rows.reduce((total, row) => total + pick(row), 0);
}

/** A series is empty when it has no rows or only zeros (gap-filled ranges). */
export function isAllZero<T>(rows: T[], pick: (row: T) => number): boolean {
  return rows.every((row) => pick(row) === 0);
}

/** Tick spacing for a daily axis so labels do not collide at phone width. */
export function tickInterval(count: number, maxTicks: number): number {
  const result = Math.max(0, Math.ceil(count / maxTicks) - 1);

  return result;
}

export interface PlatformSeries {
  platform: string;
  total: number;
  peak: number;
  points: { day: string; value: number }[];
}

/** Platforms response daily rows -> one series per platform (small multiples). */
export function platformSeries(platforms: string[], daily: { day: string; counts: Record<string, number> }[]): PlatformSeries[] {
  return platforms.map((platform) => {
    const points = daily.map((row) => ({ day: row.day, value: row.counts[platform] ?? 0 }));
    const values = points.map((point) => point.value);

    return {
      platform,
      total: sumBy(values, (value) => value),
      peak: Math.max(0, ...values),
      points,
    };
  });
}

/** Share of last month's active chats that are active again this month. */
export function retentionRate(retained: number, lastMonth: number): string {
  return formatShare(retained, lastMonth);
}
