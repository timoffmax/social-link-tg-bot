import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { BotHeartbeat, OpenSettingRequest, SecretsStatus, SettingRequest, SettingRow } from '../src/api.ts';
import {
  APPROVAL_ACTION,
  APPROVAL_CONFLICT_TEXT,
  APPROVAL_HINT,
  approvalHint,
  APPROVAL_TEXT,
  changeNeedsApproval,
  clockOffset,
  CONFLICT_TEXT,
  conflictText,
  confirmText,
  expiryText,
  formatValue,
  hostError,
  isQueueStuck,
  minutesLeft,
  missingTokenText,
  moveItem,
  needsApproval,
  NO_APPROVER_TEXT,
  pollDelay,
  REQUEST_POLL_FAST_MS,
  REQUEST_EXPIRY_GRACE_MS,
  REQUEST_POLL_SLOW_MS,
  requestStatusText,
  serverNow,
  shouldPollRequest,
  STUCK_APPROVAL_CONFLICT_TEXT,
  STUCK_CONFLICT_TEXT,
  upperBound,
  validateValue,
} from '../src/settings.ts';

function setting(overrides: Partial<SettingRow>): SettingRow {
  return {
    key: 'test.key',
    group: 'limits',
    type: 'int',
    default: 0,
    approval: 'never',
    label: 'Test',
    impact: 'Off: nothing happens.',
    effective: { value: 0, source: 'default', env_set: false },
    stored: null,
    open_request: null,
    last_change: null,
    ...overrides,
  };
}

const BUDGET = setting({ key: 'paid.apify.monthly_budget_usd', type: 'decimal', min: 0, ceiling: 20, scale: 2, approval: 'raise' });
const PRICE = setting({ key: 'paid.hiker.price_per_request_usd', type: 'decimal', min: 0, min_exclusive: true, max: 1, scale: 6 });
const ORDER = setting({ type: 'ordered_list', enum: ['apify', 'hiker'], default: ['apify', 'hiker'] });

describe('validateValue', () => {
  it('accepts values within range and scale', () => {
    assert.equal(validateValue(BUDGET, 4.8), null);
    assert.equal(validateValue(BUDGET, 20), null);
    assert.equal(validateValue(PRICE, 0.000001), null);
  });

  it('refuses values outside the range, the ceiling or the scale', () => {
    assert.equal(validateValue(BUDGET, -1), 'must be at least 0');
    assert.equal(validateValue(BUDGET, 20.01), 'must be at most 20, the hard ceiling (raising it needs a code change)');
    assert.equal(validateValue(BUDGET, 1.234), 'must have at most 2 decimals');
    assert.equal(validateValue(PRICE, 0), 'must be above 0');
    assert.equal(validateValue(PRICE, 2), 'must be at most 1');
  });

  it('refuses a fraction for a whole number and a string for a switch', () => {
    assert.equal(validateValue(setting({}), 1.5), 'must be a whole number');
    assert.equal(validateValue(setting({ type: 'bool' }), 'true'), 'must be true or false');
  });

  it('checks the provider list', () => {
    assert.equal(validateValue(ORDER, ['hiker', 'apify']), null);
    assert.equal(validateValue(ORDER, []), 'must name at least one');
    assert.equal(validateValue(ORDER, ['apify', 'apify']), 'must not name one twice');
    assert.equal(validateValue(ORDER, ['other']), 'must be one of: apify, hiker');
  });
});

describe('hostError', () => {
  it('accepts a hostname and refuses the rest', () => {
    assert.equal(hostError('www.facebed.com'), null);
    assert.equal(hostError('localhost'), 'must have at least two labels, like example.com');
    assert.equal(hostError('10.0.0.1'), 'must be a hostname, not an IP address');
    assert.equal(hostError('https://a.com'), 'must be a hostname only (letters, digits, hyphens and dots; no scheme, port, path or user)');
    assert.equal(hostError('-a.com'), 'has a label that is empty, longer than 63 characters, or starts or ends with a hyphen');
  });
});

describe('needsApproval', () => {
  it('follows the rule of the setting', () => {
    assert.equal(needsApproval('raise', 4.8, 5), true);
    assert.equal(needsApproval('raise', 4.8, 4), false);
    assert.equal(needsApproval('lower', 0.001, 0.0005), true);
    assert.equal(needsApproval('to_true', false, true), true);
    assert.equal(needsApproval('always', 'a.com', 'b.com'), true);
    assert.equal(needsApproval('always', 'a.com', 'a.com'), false);
    assert.equal(needsApproval('never', 1, 2), false);
  });
});

describe('request polling', () => {
  it('polls fast at first, then slowly', () => {
    assert.equal(pollDelay(1_000), REQUEST_POLL_FAST_MS);
    assert.equal(pollDelay(20_000), REQUEST_POLL_SLOW_MS);
  });

  it('stops once decided or past the expiry and its grace', () => {
    const expiresAt = new Date(10_000).toISOString();
    assert.equal(shouldPollRequest('queued', expiresAt, 5_000), true);
    assert.equal(shouldPollRequest('awaiting_approval', expiresAt, 10_000), true);
    assert.equal(shouldPollRequest('awaiting_approval', expiresAt, 10_000 + REQUEST_EXPIRY_GRACE_MS), false);
    assert.equal(shouldPollRequest('applied', expiresAt, 5_000), false);
    assert.equal(shouldPollRequest('rejected', expiresAt, 5_000), false);
  });
});

function request(overrides: Partial<SettingRequest>): SettingRequest {
  return {
    id: 42,
    key: 'paid.apify.monthly_budget_usd',
    status: 'queued',
    value: 6,
    expected: 4.8,
    needs_approval: null,
    error: null,
    created_at: new Date(0).toISOString(),
    expires_at: new Date(900_000).toISOString(),
    decided_at: null,
    ...overrides,
  };
}

describe('request status', () => {
  it('counts down a change waiting for the Approve in Telegram', () => {
    const awaiting = request({ status: 'awaiting_approval', needs_approval: true });
    assert.equal(requestStatusText(awaiting, 0), 'Waiting for your Approve in Telegram (expires in 15 min)');
    assert.equal(requestStatusText(awaiting, 840_001), 'Waiting for your Approve in Telegram (expires in 1 min)');
    assert.equal(requestStatusText(awaiting, 900_000), 'Waiting for your Approve in Telegram (expiring now)');
  });

  it('says it expired once the grace has passed without an answer', () => {
    const awaiting = request({ status: 'awaiting_approval', needs_approval: true });
    const queued = request({});
    const late = 900_000 + REQUEST_EXPIRY_GRACE_MS;
    assert.equal(requestStatusText(awaiting, late), 'Expired: it was not approved in Telegram in time.');
    assert.equal(requestStatusText(queued, late), 'Expired: the bot did not pick it up in time. Is the bot running?');
  });

  it('shows the queued value and the final states', () => {
    assert.equal(requestStatusText(request({}), 0), 'Waiting for the bot to apply 6 (expires in 15 min)');
    assert.equal(requestStatusText(request({ status: 'applied', value: null }), 0), 'Applied the reset');
    assert.equal(requestStatusText(request({ status: 'rejected', error: 'rejected by the owner in Telegram' }), 0), 'Not applied: rejected by the owner in Telegram');
    assert.equal(requestStatusText(request({ status: 'expired', needs_approval: true }), 0), 'Expired: it was not approved in Telegram in time');
    assert.equal(requestStatusText(request({ status: 'expired', error: 'expired: the owner did not answer in Telegram within 15 minutes' }), 0), 'Expired: the owner did not answer in Telegram within 15 minutes');
    assert.equal(requestStatusText(request({ status: 'expired', error: 'no answer' }), 0), 'Expired: no answer');
  });

  it('words the time left', () => {
    const expiresAt = new Date(120_000).toISOString();
    assert.equal(expiryText(expiresAt, 0), 'expires in 2 min');
    assert.equal(expiryText(expiresAt, 130_000), 'expiring now');
  });
});

function heartbeat(isStale: boolean): BotHeartbeat {
  return {
    started_at: '2026-10-19T10:00:00Z',
    heartbeat_at: '2026-10-19T10:00:00Z',
    settings_loaded_at: '2026-10-19T10:00:00Z',
    settings_source: 'db',
    settings_count: 1,
    is_stale: isStale,
  };
}

function openRequest(expiresAt: string): OpenSettingRequest {
  return { id: 1, status: 'queued', value: 1, created_at: '2026-10-19T10:00:00Z', expires_at: expiresAt };
}

describe('server clock', () => {
  it('measures how far the server clock is ahead of the browser', () => {
    const generatedAt = new Date(100_000).toISOString();
    assert.equal(clockOffset(generatedAt, 40_000), 60_000);
    assert.equal(clockOffset(generatedAt, 130_000), -30_000);
  });

  it('measures no offset from an unreadable timestamp', () => {
    assert.equal(clockOffset('not a time', 40_000), 0);
  });

  it('decides the expiry by the server clock when the browser runs behind', () => {
    const expiresAt = new Date(600_000).toISOString();
    const offset = clockOffset(new Date(500_000).toISOString(), 400_000);
    const now = serverNow(offset, 550_000);
    assert.equal(now, 650_000);
    assert.equal(shouldPollRequest('queued', expiresAt, now), false);
    assert.equal(minutesLeft(expiresAt, now), 0);
  });

  it('counts the minutes left by the server clock when the browser runs ahead', () => {
    const expiresAt = new Date(600_000).toISOString();
    const offset = clockOffset(new Date(100_000).toISOString(), 400_000);
    const now = serverNow(offset, 550_000);
    assert.equal(shouldPollRequest('queued', expiresAt, now), true);
    assert.equal(minutesLeft(expiresAt, now), 6);
  });
});

describe('conflict text', () => {
  const expiresAt = new Date(600_000).toISOString();

  it('keeps the normal text while the bot runs and the request has time', () => {
    const isStuck = isQueueStuck(heartbeat(false), openRequest(expiresAt), 500_000);
    assert.equal(isStuck, false);
    assert.equal(conflictText(isStuck, 'queued'), CONFLICT_TEXT);
    assert.equal(conflictText(isStuck, null), CONFLICT_TEXT);
  });

  it('asks to start the bot when it is stale or never reported', () => {
    assert.equal(isQueueStuck(heartbeat(true), openRequest(expiresAt), 500_000), true);
    assert.equal(isQueueStuck(heartbeat(true), null, 500_000), true);
    assert.equal(isQueueStuck(null, null, 500_000), true);
    assert.equal(conflictText(true, 'queued'), STUCK_CONFLICT_TEXT);
  });

  it('points to Telegram when the waiting change awaits the approval', () => {
    assert.equal(conflictText(false, 'awaiting_approval'), APPROVAL_CONFLICT_TEXT);
    assert.equal(conflictText(true, 'awaiting_approval'), STUCK_APPROVAL_CONFLICT_TEXT);
  });

  it('asks to start the bot when the open request is past its expiry', () => {
    assert.equal(isQueueStuck(heartbeat(false), openRequest(expiresAt), 600_000), true);
    assert.equal(isQueueStuck(heartbeat(false), null, 700_000), false);
  });
});

describe('texts', () => {
  it('confirms turning a switch off with its impact', () => {
    const text = confirmText(setting({ type: 'bool', label: 'TikTok', impact: 'Off: TikTok links are ignored in every chat.' }), true, false);
    assert.deepEqual(text, { question: 'Turn off TikTok?', effect: 'Off: TikTok links are ignored in every chat.', action: 'Turn off TikTok', approval: '', change: '', blocked: false });
  });

  it('blocks an approval-needing change when the bot has nobody to ask', () => {
    const noAdmin = { admin_user_id: false } as SecretsStatus;
    const withAdmin = { admin_user_id: true } as SecretsStatus;
    const blocked = confirmText(BUDGET, 4.8, 6, noAdmin);
    assert.equal(blocked.blocked, true);
    assert.equal(blocked.approval, NO_APPROVER_TEXT);
    assert.equal(confirmText(BUDGET, 4.8, 6, withAdmin).blocked, false);
    assert.equal(confirmText(BUDGET, 4.8, 4, noAdmin).blocked, false);
    assert.equal(approvalHint(noAdmin), NO_APPROVER_TEXT);
    assert.equal(approvalHint(withAdmin), APPROVAL_HINT);
    assert.equal(approvalHint(null), APPROVAL_HINT);
  });

  it('sends an approval-needing change with old and new values', () => {
    const text = confirmText(setting({ ...BUDGET, label: 'Apify budget', effective: { value: 4.8, source: 'env', env_set: true } }), 4.8, 6);
    assert.equal(text.approval, APPROVAL_TEXT);
    assert.equal(text.change, 'Apify budget: 4.8 → 6');
    assert.equal(text.action, APPROVAL_ACTION);
    assert.equal(text.question, 'Set Apify budget to 6? It is 4.8 now.');
  });

  it('confirms a direct change without the approval note', () => {
    const text = confirmText(BUDGET, 4.8, 4);
    assert.equal(text.approval, '');
    assert.equal(text.change, '');
    assert.equal(text.action, 'Set to 4');
  });

  it('knows a reset needs approval only when the value it leads to is known', () => {
    const fromDefault = setting({ ...BUDGET, default: 10, effective: { value: 4.8, source: 'db', env_set: false } });
    const fromEnv = setting({ ...BUDGET, default: 10, effective: { value: 4.8, source: 'db', env_set: true } });
    assert.equal(changeNeedsApproval(fromDefault, 4.8, null), true);
    assert.equal(changeNeedsApproval(fromEnv, 4.8, null), false);
    assert.equal(confirmText(fromDefault, 4.8, null).change, 'Test: 4.8 → 10');
  });

  it('names the fallback of a reset', () => {
    const text = confirmText(setting({ default: 0, label: 'Limit' }), 30, null);
    assert.equal(text.question, 'Reset Limit? The bot then uses the default (0) instead of 30.');
  });

  it('formats values', () => {
    assert.equal(formatValue(true), 'On');
    assert.equal(formatValue(['apify', 'hiker']), 'apify → hiker');
    assert.equal(formatValue(null), '—');
  });

  it('warns about a missing provider token only', () => {
    const secrets = { tg_bot_token: true, admin_user_id: true, apify_token: true, hiker_token: false, proxies: 0, relay_url: false, relay_token_file: false };
    assert.equal(missingTokenText(BUDGET, secrets), null);
    assert.match(missingTokenText(setting({ key: 'paid.hiker.monthly_budget_usd' }), secrets) ?? '', /HIKERAPI_TOKEN/);
  });
});

describe('helpers', () => {
  it('caps the upper bound at the ceiling', () => {
    assert.equal(upperBound(setting({ max: 1000, ceiling: 500 })), 500);
    assert.equal(upperBound(setting({})), undefined);
  });

  it('moves a list entry within bounds', () => {
    assert.deepEqual(moveItem(['a', 'b'], 1, -1), ['b', 'a']);
    assert.deepEqual(moveItem(['a', 'b'], 0, -1), ['a', 'b']);
  });
});
