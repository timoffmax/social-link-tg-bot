import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { describe, it } from 'node:test';
import type { ApifyService, HikerService, LimitRow, PublishedComponent, RelayHistory, RelayStatus, SettingRequest } from '../src/api.ts';
import {
  aboveCeilingNote,
  APPLY_NOTE,
  APPROVAL_NOTE,
  accountLimitText,
  apifySpendText,
  BLOCK_USER_WARNING,
  breakerText,
  canChange,
  ceilingNote,
  ceilingReachedNote,
  formatBytes,
  formatDuration,
  formatRate,
  globalCapNote,
  GROUP_OFF_WARNING,
  hikerSpendText,
  LIMIT_CONFLICT_TEXT,
  limitChangeText,
  limitConfirm,
  limitErrorText,
  limitTargetKey,
  limitText,
  matchesFilter,
  NOT_SUPPORTED_TEXT,
  paidStateText,
  paidTone,
  parseLimitInput,
  parseScopeId,
  PROXY_REFUSED_TEXT,
  pausedFeatureLines,
  quickTargets,
  relayCardState,
  relayHistoryByPlatform,
  rowName,
  specialLimitLabel,
  specialValueText,
  STALE_BOT_TEXT,
  stateLabel,
  UNLIMITED_NOTE,
  UNLIMITED_ON_WARNING,
  unlimitedSwitch,
  usageAge,
  WAITING_TEXT,
  WRITE_GUARD_TEXT,
} from '../src/limits.ts';

const RULES = { kind: 'special_limit', min: 0, max: 1000 };
const DEFAULTS = { user: 20, chat: 50 };
const NOW = Date.parse('2026-10-15T12:00:00Z');

function row(overrides: Partial<LimitRow> = {}): LimitRow {
  return {
    scope: 'user',
    scope_id: 7,
    title: 'alice',
    used: 12,
    used_last_month: 4,
    stored_limit: null,
    limit_source: 'default',
    effective_limit: 20,
    remaining: 8,
    enabled: true,
    state: 'ok',
    credits: 0,
    boosts: 0,
    open_request: null,
    last_change: null,
    ...overrides,
  };
}

function apify(overrides: Partial<ApifyService> = {}): ApifyService {
  return {
    has_token: true,
    is_listed: true,
    budget_usd: 10,
    used_usd: 4.12,
    in_flight_usd: 0,
    usage_read_at: '2026-10-15T11:48:00Z',
    usage_error_at: null,
    account_limit_usd: null,
    cycle_start: '2026-10-01T00:00:00Z',
    cycle_end: '2026-10-31T23:59:59Z',
    state: 'on',
    paused_features: [],
    ...overrides,
  };
}

function hiker(overrides: Partial<HikerService> = {}): HikerService {
  return {
    has_token: true,
    is_listed: true,
    budget_usd: 5,
    price_per_request_usd: 0.002,
    spent_usd: 1.23,
    in_flight_usd: 0,
    calls: 640,
    charged_calls: 617,
    gated_calls: 500,
    story_calls: 117,
    balance_requests: 1520,
    balance_read_at: '2026-10-15T11:20:00Z',
    exhausted: false,
    state: 'on',
    ...overrides,
  };
}

const HISTORY: RelayHistory = {
  windows: ['24h', '7d', '30d'],
  rows: [
    { platform: 'youtube', code: 'OK', '24h': 9, '7d': 40, '30d': 100 },
    { platform: 'youtube', code: 'BOT_CHECK', '24h': 1, '7d': 0, '30d': 3 },
    { platform: 'instagram', code: 'TRANSPORT', '24h': 0, '7d': 2, '30d': 2 },
  ],
  ok_bytes: { '24h': 1024, '7d': 2048 },
  ok_avg_ms: { '24h': 8400, '7d': null },
};

function relay(overrides: Partial<RelayStatus> = {}, isStale = false): PublishedComponent<RelayStatus> {
  const data: RelayStatus = {
    configured: true,
    platforms_in_use: ['youtube'],
    checked_at: '2026-10-15T11:59:00Z',
    reachable: true,
    error: '',
    yt_dlp: '2026.10.10',
    breaker_open: [],
    global: { hour: 1, day: 2, hourly_limit: 30, daily_limit: 150 },
    platforms: {},
    history: HISTORY,
    ...overrides,
  };

  return { data, updated_at: '2026-10-15T11:59:00Z', is_stale: isStale };
}

describe('quickTargets', () => {
  it('offers +10 and +50 on the current limit', () => {
    assert.deepEqual(quickTargets(row(), RULES), [30, 70]);
  });

  it('caps at the max and offers each target once', () => {
    assert.deepEqual(quickTargets(row({ effective_limit: 995, stored_limit: 995, limit_source: 'custom' }), RULES), [1000]);
  });

  it('offers nothing at the ceiling, above it or for unlimited', () => {
    assert.deepEqual(quickTargets(row({ effective_limit: 1000, stored_limit: 1000, limit_source: 'custom' }), RULES), []);
    assert.deepEqual(quickTargets(row({ effective_limit: 1500, stored_limit: 1500, limit_source: 'custom' }), RULES), []);
    assert.deepEqual(quickTargets(row({ effective_limit: -1, stored_limit: -1, limit_source: 'unlimited', state: 'unlimited' }), RULES), []);
  });

  it('starts from 0 while the default is unknown', () => {
    assert.deepEqual(quickTargets(row({ effective_limit: null }), RULES), [10, 50]);
  });
});

describe('parseLimitInput', () => {
  it('accepts a trimmed whole number in range', () => {
    assert.deepEqual(parseLimitInput(' 30 ', RULES), { value: 30, error: null });
    assert.deepEqual(parseLimitInput('0', RULES), { value: 0, error: null });
  });

  it('refuses empty, negative, decimal and too large values', () => {
    for (const text of ['', '-1', '10.5', '1001', 'ten']) {
      assert.deepEqual(parseLimitInput(text, RULES), { value: null, error: 'must be a whole number from 0 to 1000' }, text);
    }
  });
});

describe('limitText', () => {
  it('describes every state', () => {
    assert.equal(limitText(row()), '12 of 20 (default)');
    assert.equal(limitText(row({ stored_limit: 50, effective_limit: 50, limit_source: 'custom' })), '12 of 50');
    assert.equal(limitText(row({ state: 'unlimited', limit_source: 'unlimited', effective_limit: -1 })), '12 · unlimited');
    assert.equal(limitText(row({ scope: 'chat', state: 'off', enabled: false })), 'Off');
    assert.equal(limitText(row({ scope: 'chat', state: 'off', stored_limit: 0, effective_limit: 0 })), 'Off (limit 0)');
    assert.equal(limitText(row({ state: 'blocked', stored_limit: 0, effective_limit: 0 })), 'Blocked (limit 0)');
    assert.equal(limitText(row({ effective_limit: null })), '12 of ?');
    assert.equal(limitText(row({ state: 'full', used: 20 })), '20 of 20 (default)');
  });

  it('labels the states', () => {
    assert.equal(stateLabel('near'), 'Near the limit');
    assert.equal(stateLabel('full'), 'Full');
  });
});

describe('limitConfirm', () => {
  it('asks to raise', () => {
    const confirm = limitConfirm(row(), 30, DEFAULTS);
    assert.equal(confirm.question, 'Raise the monthly special downloads of @alice from the default (20) to 30?');
    assert.equal(confirm.action, 'Raise to 30');
    assert.equal(confirm.danger, false);
    assert.equal(confirm.warning, null);
  });

  it('warns when lowering below what is used', () => {
    const confirm = limitConfirm(row({ stored_limit: 40, effective_limit: 40, limit_source: 'custom' }), 10, DEFAULTS, '1 November 2026');
    assert.equal(confirm.action, 'Lower to 10');
    assert.equal(confirm.danger, true);
    assert.match(confirm.warning ?? '', /They used 12 this month already, .* until 1 November 2026/);
  });

  it('turns a group off at 0', () => {
    const confirm = limitConfirm(row({ scope: 'chat', scope_id: -1001, title: 'Meme Lab', effective_limit: 50 }), 0, DEFAULTS);
    assert.equal(confirm.action, 'Turn off');
    assert.equal(confirm.warning, GROUP_OFF_WARNING);
    assert.equal(confirm.danger, true);
  });

  it('blocks a person at 0', () => {
    const confirm = limitConfirm(row(), 0, DEFAULTS);
    assert.equal(confirm.action, 'Block');
    assert.equal(confirm.warning, BLOCK_USER_WARNING);
  });

  it('resets to the default', () => {
    const confirm = limitConfirm(row({ title: '', stored_limit: 80, effective_limit: 80, limit_source: 'custom' }), null, DEFAULTS);
    assert.equal(confirm.question, 'Set person 7 back to the default (20 a month)?');
    assert.equal(confirm.action, 'Reset to default');
    assert.equal(confirm.danger, false);
    assert.equal(limitConfirm(row({ used: 30, stored_limit: 80, effective_limit: 80, limit_source: 'custom' }), null, DEFAULTS).danger, true);
  });
});

describe('unlimited switch', () => {
  const rules = { ...RULES, unlimited: true };
  const unlimitedRow = row({ stored_limit: -1, effective_limit: -1, limit_source: 'unlimited', state: 'unlimited', used: 40 });

  it('offers on for a limited row, off for an unlimited one, nothing on an older bot', () => {
    assert.equal(unlimitedSwitch(row(), rules), 'on');
    assert.equal(unlimitedSwitch(unlimitedRow, rules), 'off');
    assert.equal(unlimitedSwitch(row(), RULES), null);
    assert.equal(unlimitedSwitch(row(), null), null);
  });

  it('asks to make a row unlimited', () => {
    const confirm = limitConfirm(row(), -1, DEFAULTS);
    assert.equal(confirm.question, 'Make the special downloads of @alice unlimited? The limit is the default (20) now.');
    assert.equal(confirm.action, 'Ask to make unlimited');
    assert.equal(confirm.warning, UNLIMITED_ON_WARNING);
    assert.equal(confirm.note, APPROVAL_NOTE);
    assert.equal(confirm.danger, false);
  });

  it('asks to turn unlimited off, warning when the default is already used up', () => {
    const confirm = limitConfirm(unlimitedRow, null, DEFAULTS);
    assert.equal(confirm.question, 'Turn off unlimited for @alice? They go back to the default (20 a month).');
    assert.equal(confirm.action, 'Turn off unlimited');
    assert.equal(confirm.note, APPLY_NOTE);
    assert.equal(confirm.danger, true);
  });

  it('lowers an unlimited row to a number', () => {
    assert.equal(limitConfirm(unlimitedRow, 100, DEFAULTS).action, 'Lower to 100');
  });
});

describe('canChange', () => {
  const page = { applying: true, rules: RULES };
  const request = { id: 1, kind: 'special_limit', key: 'user:7', status: 'queued' } as SettingRequest;

  it('gives the reason the actions are off, in order', () => {
    assert.equal(canChange(row(), page), null);
    assert.equal(canChange(row(), { ...page, applying: false }), STALE_BOT_TEXT);
    assert.equal(canChange(row(), { ...page, rules: null }), NOT_SUPPORTED_TEXT);
    assert.equal(canChange(row({ limit_source: 'unlimited' }), page), UNLIMITED_NOTE);
    assert.equal(canChange(row({ open_request: request }), page), WAITING_TEXT);
  });

  it('lets an unlimited row be changed when the bot takes unlimited', () => {
    const unlimitedRow = row({ stored_limit: -1, effective_limit: -1, limit_source: 'unlimited', state: 'unlimited' });
    assert.equal(canChange(unlimitedRow, { ...page, rules: { ...RULES, unlimited: true } }), null);
  });

  it('notes the ceiling', () => {
    assert.equal(ceilingNote(row({ effective_limit: 1000, stored_limit: 1000, limit_source: 'custom' }), RULES), ceilingReachedNote(1000));
    assert.equal(ceilingNote(row({ effective_limit: 1500, stored_limit: 1500, limit_source: 'custom' }), RULES), aboveCeilingNote(1000));
    assert.equal(ceilingNote(row(), RULES), null);
  });
});

describe('paidStateText', () => {
  it('describes every state and reason', () => {
    assert.equal(paidStateText(apify()), 'Running');
    assert.equal(paidStateText(apify({ paused_features: ['shorts'] })), 'Running · Shorts paused');
    assert.equal(paidStateText(apify({ state: 'paused', reason: 'budget_used' })), 'Paused: monthly budget used up');
    assert.equal(paidStateText(apify({ state: 'paused', reason: 'budget_zero' })), 'Paused: budget is $0');
    assert.equal(paidStateText(apify({ state: 'paused', reason: 'usage_unreadable' })), 'Paused: Apify usage can’t be read, runs are refused until it can');
    assert.equal(paidStateText(apify({ state: 'unknown', reason: 'usage_not_read' })), 'Usage not read yet (checked before each run)');
    assert.equal(paidStateText(hiker({ state: 'paused', reason: 'balance_empty' })), 'Paused: HikerAPI balance is empty (rechecked hourly)');
    assert.equal(paidStateText(hiker({ state: 'unknown', reason: 'ledger_error' })), 'Spend unknown: the ledger could not be read');
    assert.equal(paidStateText(hiker({ state: 'off', reason: 'no_token' })), 'Off: no token in .env');
    assert.equal(paidStateText(hiker({ state: 'off', reason: 'not_listed' })), 'Not used: not in the provider order');
  });

  it('picks a tone', () => {
    assert.equal(paidTone(apify()), 'ok');
    assert.equal(paidTone(apify({ paused_features: ['shorts'] })), 'warning');
    assert.equal(paidTone(hiker({ state: 'paused' })), 'danger');
    assert.equal(paidTone(hiker({ state: 'off' })), 'muted');
  });

  it('describes the spend', () => {
    assert.equal(apifySpendText(apify()), '$4.12 of $10.00 · Apify billing cycle (1 Oct – 31 Oct)');
    assert.equal(apifySpendText(apify({ cycle_start: null })), '$4.12 of $10.00 · Apify billing cycle');
    assert.equal(hikerSpendText(hiker()), '$1.23 of $5.00 this month (UTC) · 617 billed calls (500 posts, 117 stories) · $0.002 a call');
    assert.equal(accountLimitText(apify({ account_limit_usd: 8 })), 'Apify account limit: $8.00 (below the budget)');
    assert.equal(accountLimitText(apify({ account_limit_usd: 25 })), null);
    assert.deepEqual(pausedFeatureLines(apify({ used_usd: 9.92, paused_features: ['shorts'] })), ['Shorts paused: only $0.08 left, less than one run may cost']);
  });

  it('tells the age of a read', () => {
    assert.equal(usageAge('2026-10-15T11:48:00Z', NOW), 'read 12 min ago');
    assert.equal(usageAge('2026-10-15T11:59:40Z', NOW), 'read just now');
    assert.equal(usageAge('2026-10-15T07:00:00Z', NOW), 'read 5 h ago');
    assert.equal(usageAge(null, NOW), 'not read yet');
  });
});

describe('matchesFilter', () => {
  it('filters by scope and searches name, @username and id', () => {
    assert.equal(matchesFilter(row(), 'all', ''), true);
    assert.equal(matchesFilter(row(), 'chat', ''), false);
    assert.equal(matchesFilter(row(), 'user', '@ALI'), true);
    assert.equal(matchesFilter(row({ scope: 'chat', scope_id: -1001234 }), 'all', '-1001'), true);
    assert.equal(matchesFilter(row(), 'all', 'bob'), false);
  });

  it('names a row', () => {
    assert.equal(rowName(row()), '@alice');
    assert.equal(rowName(row({ scope: 'chat', scope_id: -5, title: '' })), 'group -5');
  });
});

describe('specialLimitLabel', () => {
  it('labels limit audit keys only', () => {
    assert.equal(specialLimitLabel('special_limit:user:123'), 'Special-downloads limit · person 123');
    assert.equal(specialLimitLabel('special_limit:chat:-1001'), 'Special-downloads limit · group -1001');
    assert.equal(specialLimitLabel('special_enabled:chat:-1001'), 'Special downloads on/off · group -1001');
    assert.equal(specialLimitLabel('gated.user_monthly_allowance'), null);
  });

  it('shows a limit value', () => {
    assert.equal(specialValueText(null), 'default');
    assert.equal(specialValueText(-1), 'unlimited');
    assert.equal(specialValueText(false), 'Off');
    assert.equal(specialValueText(30), '30');
  });

  it('describes the last change', () => {
    const change = { at: '2026-10-18T09:04:00Z', actor: 'dashboard' as const, old_value: null, new_value: 30 };
    assert.equal(limitChangeText(change, 20, 'the dashboard', '18 Oct, 12:04'), 'Raised the default (20) → 30 by the dashboard, 18 Oct, 12:04');
    assert.match(limitChangeText({ ...change, old_value: -1, new_value: 100 }, 20, 'Telegram', 'now'), /^Lowered unlimited → 100/);
  });
});

describe('limitErrorText', () => {
  it('maps the refusals', () => {
    assert.equal(limitErrorText(409, 'the running bot does not take limit changes yet: deploy the bot first', true, true), NOT_SUPPORTED_TEXT);
    assert.equal(limitErrorText(409, 'a change of this limit is already waiting', true, true), LIMIT_CONFLICT_TEXT);
    assert.equal(limitErrorText(409, 'a change of this limit is already waiting', false, true), `${LIMIT_CONFLICT_TEXT} ${STALE_BOT_TEXT}`);
    assert.equal(limitErrorText(403, 'forbidden', true, true), WRITE_GUARD_TEXT);
    assert.equal(limitErrorText(403, 'Request failed with HTTP 403', true, false), PROXY_REFUSED_TEXT);
    assert.equal(limitErrorText(400, 'scope must be user or chat', true, true), 'Not sent: scope must be user or chat.');
  });

  it('checks the sign of a typed id', () => {
    assert.deepEqual(parseScopeId(' -1001234567890 ', 'chat'), { id: -1001234567890, error: null });
    assert.equal(parseScopeId('5', 'chat').id, null);
    assert.equal(parseScopeId('-5', 'user').id, null);
    assert.equal(parseScopeId('12.5', 'user').id, null);
    assert.equal(parseScopeId('99999999999999999999', 'user').id, null);
  });

  it('notes the bot-wide cap', () => {
    assert.equal(globalCapNote({ used: 3, cap: 400, off: false }), 'of 400');
    assert.equal(globalCapNote({ used: 3, cap: null, off: false }), 'no cap');
    assert.equal(globalCapNote({ used: 3, cap: 0, off: true }), 'off');
  });
});

describe('relayCardState', () => {
  it('is reachable after a fresh check', () => {
    assert.deepEqual(relayCardState(relay(), false, NOW), { kind: 'reachable', label: 'Reachable', tone: 'ok' });
  });

  it('is unreachable when the last check failed', () => {
    assert.equal(relayCardState(relay({ reachable: false, error: 'connection refused' }), false, NOW).kind, 'unreachable');
  });

  it('warns about an open breaker', () => {
    const state = relayCardState(relay({ breaker_open: ['instagram'] }), false, NOW);
    assert.equal(state.tone, 'warning');
    assert.equal(state.label, 'Reachable · breaker open: instagram');
  });

  it('is not configured, or waiting before the bot reported', () => {
    assert.equal(relayCardState(relay({ configured: false }), false, NOW).kind, 'not_configured');
    assert.equal(relayCardState(null, false, NOW).kind, 'waiting');
  });

  it('is stale with an old check, an old component or a stale bot', () => {
    assert.equal(relayCardState(relay({ checked_at: '2026-10-15T11:50:00Z' }), false, NOW).kind, 'stale');
    assert.equal(relayCardState(relay({}, true), false, NOW).kind, 'stale');
    assert.equal(relayCardState(relay(), true, NOW).kind, 'stale');
  });

  it('groups the history by platform with success rates', () => {
    const history = relayHistoryByPlatform(HISTORY);
    const youtube = history.find((entry) => (entry.platform === 'youtube'));
    const instagram = history.find((entry) => (entry.platform === 'instagram'));
    assert.deepEqual(history.map((entry) => entry.platform), ['instagram', 'youtube']);
    assert.deepEqual(youtube?.windows['24h'], { ok: 9, failed: 1, rate: 0.9, failures: [{ code: 'BOT_CHECK', count: 1 }] });
    assert.equal(youtube?.windows['7d'].failed, 0);
    assert.equal(instagram?.windows['24h'].rate, null);
    assert.equal(formatRate(0.9), '90%');
  });

  it('formats breakers, bytes and durations', () => {
    assert.equal(breakerText({ enabled: true, breaker: 'open', breaker_left_s: 230, hour: 0, day: 0, hourly_limit: 1, daily_limit: 1 }), 'open, 4 min left');
    assert.equal(breakerText({ enabled: true, breaker: 'closed', breaker_left_s: 0, hour: 0, day: 0, hourly_limit: 1, daily_limit: 1 }), 'closed');
    assert.equal(formatBytes(1536), '1.5 KB');
    assert.equal(formatBytes(512), '512 B');
    assert.equal(formatDuration(8400), '8.4 s');
    assert.equal(formatDuration(null), '—');
  });
});

describe('limitTargetKey', () => {
  it('gives each person and group its own key', () => {
    assert.equal(limitTargetKey('chat', -1001), 'chat:-1001');
    assert.notEqual(limitTargetKey('chat', -1001), limitTargetKey('chat', -1002));
    assert.notEqual(limitTargetKey('user', 5), limitTargetKey('chat', 5));
  });

  it('keys the found row by its target, so a new find starts with fresh row state', () => {
    const source = readFileSync(new URL('../src/tabs/Limits.tsx', import.meta.url), 'utf8');

    assert.match(source, /<FoundLimit key=\{limitTargetKey\(found\.scope, found\.id\)\}/);
  });
});
