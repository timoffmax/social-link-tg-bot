// Pure helpers for the Settings tab (type-only imports, so `node --test` runs them as is). The checks
// mirror the bot's registry validation for instant feedback; the bot validates again and decides.
import type {
  ApprovalRule,
  BotHeartbeat,
  OpenSettingRequest,
  SecretsStatus,
  SettingActor,
  SettingRequest,
  SettingRequestStatus,
  SettingRow,
  SettingSource,
  SettingValue,
} from './api';

/** The page checks a queued change every second for 15 s, then every 5 s. */
export const REQUEST_POLL_FAST_MS = 1_000;
export const REQUEST_POLL_SLOW_MS = 5_000;
export const REQUEST_POLL_FAST_FOR_MS = 15_000;

/** Polling goes on this long past a change's expiry, so the bot's own "expired" is seen. */
export const REQUEST_EXPIRY_GRACE_MS = 10_000;

/** The Settings tab reloads every 30 s while it is visible. */
export const SETTINGS_REFRESH_MS = 30_000;

/** An applied change's check mark fades after this. */
export const APPLIED_SHOWN_MS = 4_000;

const MS_PER_MINUTE = 60_000;

export const CONFLICT_TEXT = 'A change of this setting is already waiting. Wait for it to finish, then try again.';
export const APPROVAL_TEXT = 'This change needs your approval in Telegram: the bot will send you Approve / Reject.';
export const APPROVAL_HINT = 'Saving sends this change to Telegram for your Approve.';
export const APPROVAL_ACTION = 'Send for approval';
export const STUCK_CONFLICT_TEXT = 'A change of this setting is still queued and only the running bot can clear it. Start the bot, then try again.';
export const APPROVAL_CONFLICT_TEXT = 'A change of this setting is waiting for your Approve in Telegram. Approve or Reject it there, then try again.';
export const STUCK_APPROVAL_CONFLICT_TEXT = 'A change of this setting is waiting for your Approve in Telegram, and only the running bot can record your answer or its expiry. Start the bot, then try again.';
export const NO_APPROVER_TEXT = 'This change needs your approval in Telegram, but ADMIN_USER_ID is not set in .env, so the bot can not ask you and would refuse it. Set ADMIN_USER_ID and restart the bot first.';

/** The prefix of the bot's expiry reasons; the status line already says "Expired". */
const EXPIRED_REASON_PREFIX = 'expired: ';

const HOST_MAX_LENGTH = 253;
const HOST_LABEL_MAX_LENGTH = 63;
const HOST_MIN_LABELS = 2;
const HOST_CHARACTERS = /^[a-z0-9.-]+$/;
const ALL_DIGITS = /^[0-9]+$/;
/** An IPv6 literal as net.ParseIP reads it: hex digits, colons and an optional IPv4 tail. */
const IPV6_LITERAL = /^[0-9a-f]*:[0-9a-f:.]*$/;
const SCALE_TOLERANCE = 1e-9;

export const GROUP_LABELS: Record<string, string> = {
  platforms: 'Platforms',
  services: 'Free services',
  paid: 'Paid providers & budgets',
  restricted_posts: 'Special downloads limits',
  stars: 'Stars support',
  limits: 'Limits',
};

export function groupLabel(group: string): string {
  return GROUP_LABELS[group] ?? group;
}

export const SOURCE_LABELS: Record<SettingSource, { label: string; hint: string }> = {
  db: { label: 'Set here', hint: 'Set in the dashboard; stored in the bot’s database' },
  cache: { label: 'Cached', hint: 'The value set here, from the bot’s cache file: the database was down when the bot started' },
  safe: { label: 'Safe value', hint: 'The stored value is no longer valid, so the bot uses the safe value until it is changed' },
  env: { label: '.env', hint: 'From the bot’s .env file' },
  default: { label: 'Default', hint: 'Neither set here nor in .env: the code default' },
};

const ACTOR_LABELS: Record<SettingActor, string> = {
  dashboard: 'the dashboard',
  telegram: 'Telegram',
  bot: 'the bot',
  manual: 'a manual database edit',
};

export function actorLabel(actor: SettingActor): string {
  return ACTOR_LABELS[actor] ?? actor;
}

export const OPEN_STATUSES: SettingRequestStatus[] = ['queued', 'awaiting_approval'];

export function isOpenStatus(status: SettingRequestStatus): boolean {
  return OPEN_STATUSES.includes(status);
}

/** How long to wait before checking a queued change again, by the time since it was queued. */
export function pollDelay(elapsedMs: number): number {
  const result = (elapsedMs < REQUEST_POLL_FAST_FOR_MS) ? REQUEST_POLL_FAST_MS : REQUEST_POLL_SLOW_MS;

  return result;
}

/**
 * Whether to check a change again: it is still open and its expiry (plus a short grace) has not
 * passed. A stopped bot never decides a change, so the page stops then instead of waiting forever.
 */
export function shouldPollRequest(status: SettingRequestStatus, expiresAt: string, now: number): boolean {
  const isOpen = isOpenStatus(status);
  const deadline = Date.parse(expiresAt) + REQUEST_EXPIRY_GRACE_MS;
  const hasTime = (now < deadline);

  return isOpen && hasTime;
}

/** How far the server clock is ahead of the browser's: generated_at minus the browser time it arrived at. */
export function clockOffset(generatedAt: string, loadedAt: number = Date.now()): number {
  const serverTime = Date.parse(generatedAt);
  const result = Number.isNaN(serverTime) ? 0 : (serverTime - loadedAt);

  return result;
}

/** The server's time now, from the browser clock and the offset measured at load. */
export function serverNow(offset: number, clientNow: number = Date.now()): number {
  return (clientNow + offset);
}

/**
 * Whether a waiting change can only be cleared by a running bot: the bot is down (stale or never
 * reported) or the open request is past its expiry, which only the bot marks.
 */
export function isQueueStuck(bot: BotHeartbeat | null, openRequest: OpenSettingRequest | null, now: number): boolean {
  const isBotDown = (bot === null) || (bot.is_stale === true);
  const isOverdue = (openRequest !== null) && (now >= Date.parse(openRequest.expires_at));

  return (isBotDown || isOverdue);
}

/** The text of a 409: a change of the key is already waiting (status: the open one's, null when unknown). */
export function conflictText(isStuck: boolean, status: SettingRequestStatus | null): string {
  const isAwaiting = (status === 'awaiting_approval');
  const queuedText = isStuck ? STUCK_CONFLICT_TEXT : CONFLICT_TEXT;
  const awaitingText = isStuck ? STUCK_APPROVAL_CONFLICT_TEXT : APPROVAL_CONFLICT_TEXT;

  return isAwaiting ? awaitingText : queuedText;
}

/** Whether the bot can ask the owner in Telegram: ADMIN_USER_ID is set, or the page does not know. */
export function canAskOwner(secrets: SecretsStatus | null): boolean {
  return (secrets?.admin_user_id !== false);
}

/** The note under a change that needs the owner's approval. */
export function approvalHint(secrets: SecretsStatus | null): string {
  return canAskOwner(secrets) ? APPROVAL_HINT : NO_APPROVER_TEXT;
}

/** JSON equality of two values, as the bot compares an expected value. */
export function sameValue(left: SettingValue | null, right: SettingValue | null): boolean {
  return (JSON.stringify(left) === JSON.stringify(right));
}

/** Whether a change from current to next waits for the owner's Approve in Telegram (the bot's rule). */
export function needsApproval(rule: ApprovalRule, current: SettingValue, next: SettingValue): boolean {
  const areNumbers = (typeof current === 'number') && (typeof next === 'number');
  const rules: Record<ApprovalRule, () => boolean> = {
    never: () => false,
    always: () => (sameValue(current, next) === false),
    raise: () => ((areNumbers === false) || ((next as number) > (current as number))),
    lower: () => ((areNumbers === false) || ((next as number) < (current as number))),
    to_true: () => ((next === true) && (current !== true)),
  };

  return rules[rule]();
}

/** The highest value allowed: the lower of max and the hard ceiling. */
export function upperBound(setting: SettingRow): number | undefined {
  const bounds = [setting.max, setting.ceiling].filter((bound): bound is number => (bound !== undefined));
  const result = (bounds.length === 0) ? undefined : Math.min(...bounds);

  return result;
}

function formatBound(value: number): string {
  return String(value);
}

function rangeError(setting: SettingRow, value: number): string | null {
  const min = setting.min;
  const isMinExclusive = (setting.min_exclusive === true);
  const checks: [boolean, string][] = [
    [((min !== undefined) && (isMinExclusive === false) && (value < min)), `must be at least ${formatBound(min ?? 0)}`],
    [((min !== undefined) && isMinExclusive && (value <= min)), `must be above ${formatBound(min ?? 0)}`],
    [((setting.max !== undefined) && (value > setting.max)), `must be at most ${formatBound(setting.max ?? 0)}`],
    [((setting.ceiling !== undefined) && (value > setting.ceiling)), `must be at most ${formatBound(setting.ceiling ?? 0)}, the hard ceiling (raising it needs a code change)`],
  ];
  const failed = checks.find(([isFailed]) => isFailed);

  return (failed === undefined) ? null : failed[1];
}

function scaleError(setting: SettingRow, value: number): string | null {
  const scale = setting.scale ?? 0;
  const factor = 10 ** scale;
  const scaled = value * factor;
  const rounded = Math.round(scaled);
  const drift = Math.abs(scaled - rounded);
  const magnitude = Math.abs(scaled);
  const tolerance = SCALE_TOLERANCE * Math.max(1, magnitude);
  const isScaled = (drift <= tolerance);

  return isScaled ? null : `must have at most ${scale} decimals`;
}

function numberError(setting: SettingRow, value: SettingValue): string | null {
  const isNumber = (typeof value === 'number') && Number.isFinite(value);
  let result: string | null = isNumber ? null : 'must be a number';

  if ((result === null) && (setting.type === 'int') && (Number.isInteger(value) === false)) {
    result = 'must be a whole number';
  }

  if ((result === null) && (setting.type === 'decimal')) {
    result = scaleError(setting, value as number);
  }

  if (result === null) {
    result = rangeError(setting, value as number);
  }

  return result;
}

function enumError(setting: SettingRow): string {
  return `must be one of: ${(setting.enum ?? []).join(', ')}`;
}

/** The bot's hostname rule: lowercase letters, digits, hyphens and dots, two labels or more, no IP. */
export function hostError(host: string): string | null {
  const labels = host.split('.');
  const lastLabel = labels[labels.length - 1] ?? '';
  const isIPv6 = IPV6_LITERAL.test(host);
  const hasBadLabel = labels.some((label) => ((label.length === 0) || (label.length > HOST_LABEL_MAX_LENGTH) || label.startsWith('-') || label.endsWith('-')));
  const checks: [boolean, string][] = [
    [(host === ''), 'must not be empty'],
    [(host.length > HOST_MAX_LENGTH), 'must be at most 253 characters'],
    [(isIPv6 || ALL_DIGITS.test(lastLabel)), 'must be a hostname, not an IP address'],
    [(HOST_CHARACTERS.test(host) === false), 'must be a hostname only (letters, digits, hyphens and dots; no scheme, port, path or user)'],
    [(labels.length < HOST_MIN_LABELS), 'must have at least two labels, like example.com'],
    [hasBadLabel, 'has a label that is empty, longer than 63 characters, or starts or ends with a hyphen'],
  ];
  const failed = checks.find(([isFailed]) => isFailed);

  return (failed === undefined) ? null : failed[1];
}

function listError(setting: SettingRow, value: SettingValue): string | null {
  const isList = Array.isArray(value) && value.every((item) => (typeof item === 'string'));
  const names = isList ? (value as string[]) : [];
  const isKnown = names.every((name) => (setting.enum ?? []).includes(name));
  const isUnique = (new Set(names).size === names.length);
  const checks: [boolean, string][] = [
    [(isList === false), 'must be a list of names'],
    [(names.length === 0), 'must name at least one'],
    [(isKnown === false), enumError(setting)],
    [(isUnique === false), 'must not name one twice'],
  ];
  const failed = checks.find(([isFailed]) => isFailed);

  return (failed === undefined) ? null : failed[1];
}

/** Why the bot would refuse value for the setting, or null when it is valid. */
export function validateValue(setting: SettingRow, value: SettingValue): string | null {
  const validators: Record<SettingRow['type'], () => string | null> = {
    bool: () => ((typeof value === 'boolean') ? null : 'must be true or false'),
    int: () => numberError(setting, value),
    decimal: () => numberError(setting, value),
    enum: () => (((typeof value === 'string') && (setting.enum ?? []).includes(value)) ? null : enumError(setting)),
    host: () => ((typeof value === 'string') ? hostError(value) : 'must be text'),
    ordered_list: () => listError(setting, value),
  };

  return validators[setting.type]();
}

/** A value as the owner reads it: On / Off, numbers as given, lists in order. */
export function formatValue(value: SettingValue | null): string {
  let result = '—';

  if (typeof value === 'boolean') {
    result = value ? 'On' : 'Off';
  }

  if ((typeof value === 'number') || (typeof value === 'string')) {
    result = String(value);
  }

  if (Array.isArray(value)) {
    result = value.join(' → ');
  }

  return result;
}

/** The allowed range in words, e.g. "0 to 20, up to 2 decimals". */
export function rangeText(setting: SettingRow): string {
  const upper = upperBound(setting);
  const min = setting.min;
  const lowerText = (min === undefined) ? '' : `${(setting.min_exclusive === true) ? 'above' : 'from'} ${min}`;
  const upperText = (upper === undefined) ? '' : `up to ${upper}`;
  const decimals = ((setting.type === 'decimal') && ((setting.scale ?? 0) > 0)) ? `at most ${setting.scale} decimals` : '';

  return [lowerText, upperText, decimals].filter((part) => (part !== '')).join(', ');
}

/** The input step of a number field: 1 for whole numbers, else the smallest decimal allowed. */
export function numberStep(setting: SettingRow): string {
  const scale = (setting.type === 'decimal') ? (setting.scale ?? 0) : 0;

  return (scale === 0) ? '1' : (10 ** -scale).toFixed(scale);
}

/** Parses a number field; null when it is not a number. */
export function parseNumber(text: string): number | null {
  const trimmed = text.trim();
  const value = Number(trimmed);
  const result = ((trimmed === '') || (Number.isFinite(value) === false)) ? null : value;

  return result;
}

/** Whether a change turns something off or to zero, which the confirmation spells out. */
function isStopping(next: SettingValue | null): boolean {
  return (next === false) || (next === 0);
}

export interface ConfirmText {
  /** The question, e.g. "Turn off TikTok?". */
  question: string;
  /** What the change does, in plain words (the setting's impact). */
  effect: string;
  /** The confirm button, repeating the action. */
  action: string;
  /** The Telegram approval note, or '' when the change applies directly. */
  approval: string;
  /** Old -> new values, shown with the approval note; '' without one. */
  change: string;
  /** The change can not be sent: it needs an approval nobody can be asked for. */
  blocked: boolean;
}

/**
 * The value a change leads to: next itself, or for a reset the default when .env does not set the
 * key; null when only the bot knows it (a reset to an .env value).
 */
function targetOf(setting: SettingRow, next: SettingValue | null): SettingValue | null {
  const isEnvSet = (setting.effective?.env_set === true);
  let result: SettingValue | null = next;

  if ((next === null) && (isEnvSet === false)) {
    result = setting.default;
  }

  return result;
}

/** Whether the page can tell that a change (next null: a reset) waits for the owner's Approve. */
export function changeNeedsApproval(setting: SettingRow, current: SettingValue, next: SettingValue | null): boolean {
  const target = targetOf(setting, next);
  const result = (target !== null) && needsApproval(setting.approval, current, target);

  return result;
}

/** The confirmation of a change (next null: a reset to .env / default). */
export function confirmText(setting: SettingRow, current: SettingValue, next: SettingValue | null, secrets: SecretsStatus | null = null): ConfirmText {
  const isReset = (next === null);
  const isSwitch = (setting.type === 'bool') && (isReset === false);
  const switchWord = (next === true) ? 'on' : 'off';
  const fallback = (setting.effective?.env_set === true) ? 'its .env value' : `the default (${formatValue(setting.default)})`;
  const isApproval = changeNeedsApproval(setting, current, next);
  const target = targetOf(setting, next);
  const isBlocked = isApproval && (canAskOwner(secrets) === false);
  let result: ConfirmText = {
    question: `Set ${setting.label} to ${formatValue(next)}? It is ${formatValue(current)} now.`,
    effect: isStopping(next) ? setting.impact : '',
    action: `Set to ${formatValue(next)}`,
    approval: '',
    change: '',
    blocked: false,
  };

  if (isSwitch) {
    result = {
      ...result,
      question: `Turn ${switchWord} ${setting.label}?`,
      action: `Turn ${switchWord} ${setting.label}`,
    };
  }

  if (isReset) {
    result = {
      ...result,
      question: `Reset ${setting.label}? The bot then uses ${fallback} instead of ${formatValue(current)}.`,
      effect: '',
      action: 'Reset',
    };
  }

  if (isApproval) {
    result = {
      ...result,
      action: APPROVAL_ACTION,
      approval: APPROVAL_TEXT,
      change: `${setting.label}: ${formatValue(current)} → ${formatValue(target)}`,
    };
  }

  if (isBlocked) {
    result = { ...result, approval: NO_APPROVER_TEXT, blocked: true };
  }

  return result;
}

/** The note shown next to a paid setting whose provider has no token in .env, or null. */
export function missingTokenText(setting: SettingRow, secrets: SecretsStatus | null): string | null {
  const isApify = setting.key.startsWith('paid.apify.');
  const isHiker = setting.key.startsWith('paid.hiker.') || setting.key.startsWith('alerts.hiker_');
  let result: string | null = null;

  if ((secrets !== null) && isApify && (secrets.apify_token === false)) {
    result = 'APIFY_TOKEN is not set in .env: Apify stays off whatever the budget.';
  }

  if ((secrets !== null) && isHiker && (secrets.hiker_token === false)) {
    result = 'HIKERAPI_TOKEN is not set in .env: HikerAPI stays off whatever the budget.';
  }

  return result;
}

/** Minutes left until an expiry, at least 0, rounded up. */
export function minutesLeft(expiresAt: string, now: number): number {
  const left = Date.parse(expiresAt) - now;
  const minutes = Math.ceil(left / MS_PER_MINUTE);

  return Math.max(0, minutes);
}

/** The time left of an open change in words: "expires in N min", or "expiring now" once it is due. */
export function expiryText(expiresAt: string, now: number): string {
  const isDue = (now >= Date.parse(expiresAt));
  const result = isDue ? 'expiring now' : `expires in ${minutesLeft(expiresAt, now)} min`;

  return result;
}

/** The fallback reason of an expired change, by whether it waited for the owner's Approve. */
function expiredReason(request: SettingRequest): string {
  const result = (request.needs_approval === true) ? 'it was not approved in Telegram in time' : 'the bot did not apply it within 10 minutes';

  return result;
}

/** An expired change's line: the bot's reason without its own "expired: " prefix. */
function expiredText(request: SettingRequest): string {
  const reason = request.error ?? expiredReason(request);
  const hasPrefix = reason.startsWith(EXPIRED_REASON_PREFIX);
  const shown = hasPrefix ? reason.slice(EXPIRED_REASON_PREFIX.length) : reason;

  return `Expired: ${shown}`;
}

/** Where a followed change stands, as one line for its row. */
export function requestStatusText(request: SettingRequest, now: number): string {
  const isOverdue = isOpenStatus(request.status) && (shouldPollRequest(request.status, request.expires_at, now) === false);
  const expiry = expiryText(request.expires_at, now);
  const shownValue = (request.value === null) ? 'the reset' : formatValue(request.value);
  const texts: Record<SettingRequestStatus, string> = {
    queued: `Waiting for the bot to apply ${shownValue} (${expiry})`,
    awaiting_approval: `Waiting for your Approve in Telegram (${expiry})`,
    applied: `Applied ${shownValue}`,
    rejected: `Not applied: ${request.error ?? 'refused by the bot'}`,
    expired: expiredText(request),
  };
  const overdueTexts: Partial<Record<SettingRequestStatus, string>> = {
    queued: 'Expired: the bot did not pick it up in time. Is the bot running?',
    awaiting_approval: 'Expired: it was not approved in Telegram in time.',
  };
  let result = texts[request.status] ?? request.status;

  if (isOverdue) {
    result = overdueTexts[request.status] ?? result;
  }

  return result;
}

/** The order of a provider list after moving one entry up (-1) or down (+1); unchanged at the edges. */
export function moveItem(list: string[], index: number, step: number): string[] {
  const target = index + step;
  const result = [...list];
  const isInside = (index >= 0) && (index < list.length) && (target >= 0) && (target < list.length);

  if (isInside) {
    const moved = result[index] as string;
    result[index] = result[target] as string;
    result[target] = moved;
  }

  return result;
}
