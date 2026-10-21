import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { ServiceOffReason, ServiceRow, ServiceSignal, ServiceState } from '../src/api.ts';
import { minutesSince, PLATFORM_ORDER } from '../src/format.ts';
import {
  ageText,
  ERROR_CODE_TEXT,
  errorShown,
  errorText,
  FALLBACK_NOTE,
  groupServices,
  OFF_REASON_TEXT,
  probeText,
  serviceBadge,
  serviceKindLabel,
  servicesSubtitle,
  SHARED_GROUP,
  trafficText,
} from '../src/services.ts';

const NOW = Date.parse('2026-10-20T12:00:00Z');
const TZ = 'UTC';

function signal(overrides: Partial<ServiceSignal> = {}): ServiceSignal {
  return {
    state: 'ok',
    last_success_at: '2026-10-20T11:55:00Z',
    last_failure_at: null,
    failing_since: null,
    consecutive_failures: 0,
    threshold: 3,
    last_error_code: null,
    ...overrides,
  };
}

function row(overrides: Partial<ServiceRow> = {}): ServiceRow {
  return {
    key: 'kkinstagram',
    label: 'kkinstagram',
    kind: 'free',
    host: null,
    platforms: ['instagram'],
    role: 'Posts and reels',
    fallback: false,
    has_traffic: true,
    has_probe: true,
    state: 'ok',
    off_reason: null,
    traffic: null,
    probe: null,
    ...overrides,
  };
}

const failing = signal({
  state: 'degraded',
  last_failure_at: '2026-10-20T11:50:00Z',
  failing_since: '2026-10-20T11:10:00Z',
  consecutive_failures: 2,
  last_error_code: 'wrong_content',
});

describe('groupServices', () => {
  it('groups by platform in the platform order, unknown platforms after the known ones and Shared last', () => {
    const rows = [
      row({ key: 'pool', platforms: ['instagram', 'tiktok'] }),
      row({ key: 'mystery', platforms: ['myspace'] }),
      row({ key: 'threads', platforms: ['threads'] }),
      row({ key: 'tiktok', platforms: ['tiktok'] }),
      row({ key: 'insta', platforms: ['instagram'] }),
      row({ key: 'other', platforms: ['bebo'] }),
    ];
    const groups = groupServices(rows, PLATFORM_ORDER);

    assert.deepEqual(groups.map((group) => group.key), ['instagram', 'tiktok', 'threads', 'myspace', 'bebo', SHARED_GROUP]);
    assert.deepEqual(groups.map((group) => group.rows.map((item) => item.key)), [['insta'], ['tiktok'], ['threads'], ['mystery'], ['other'], ['pool']]);
  });

  it('orders a group worst first, then in the bot’s order', () => {
    const states: ServiceState[] = ['off', 'ok', 'idle', 'down', 'unknown', 'degraded', 'stale', 'ok', 'down'];
    const rows = states.map((state, index) => row({ key: `${state}-${index}`, state }));
    const groups = groupServices(rows, PLATFORM_ORDER);

    assert.deepEqual(groups[0]?.rows.map((item) => item.key), ['down-3', 'down-8', 'degraded-5', 'stale-6', 'ok-1', 'ok-7', 'unknown-4', 'idle-2', 'off-0']);
  });

  it('gives no groups without rows', () => {
    assert.deepEqual(groupServices([], PLATFORM_ORDER), []);
  });
});

describe('serviceBadge', () => {
  it('words every state with its tone', () => {
    const traffic = signal({ state: 'stale', last_failure_at: '2026-10-17T12:00:00Z', consecutive_failures: 1 });
    const cases: [ServiceRow, string, string][] = [
      [row({ state: 'ok' }), 'OK', 'ok'],
      [row({ state: 'degraded' }), 'Degraded', 'warning'],
      [row({ state: 'down' }), 'Down', 'danger'],
      [row({ state: 'stale', traffic }), 'Last failed 3 days ago', 'muted'],
      [row({ state: 'idle' }), 'Not needed recently', 'muted'],
      [row({ state: 'unknown' }), 'No data yet', 'muted'],
    ];

    for (const [item, text, tone] of cases) {
      assert.deepEqual(serviceBadge(item, NOW), { text, tone }, item.state);
    }
  });

  it('words every off reason, the paused budget as Paused', () => {
    const expected: Record<ServiceOffReason, string> = {
      platform_off: 'Off: platform switched off',
      not_configured: 'Off: not configured',
      not_listed: 'Off: not in the provider order',
      proxies_off: 'Off: proxies off',
      special_off: 'Off: special downloads off',
      relay_disabled: 'Off: disabled on the relay',
      budget_paused: 'Paused: budget',
    };

    for (const reason of Object.keys(OFF_REASON_TEXT) as ServiceOffReason[]) {
      assert.deepEqual(serviceBadge(row({ state: 'off', off_reason: reason }), NOW), { text: expected[reason], tone: 'muted' }, reason);
    }
  });
});

describe('trafficText', () => {
  it('shows a dash without a traffic signal and None yet before the first event', () => {
    assert.equal(trafficText(row({ has_traffic: false }), NOW, TZ), '—');
    assert.equal(trafficText(row(), NOW, TZ), 'None yet');
  });

  it('words OK, failing and aged-out traffic', () => {
    const agedOut = signal({ state: 'stale', last_failure_at: '2026-10-18T12:00:00Z', consecutive_failures: 2 });

    assert.equal(trafficText(row({ traffic: signal() }), NOW, TZ), 'OK 5 min ago');
    assert.equal(trafficText(row({ traffic: failing }), NOW, TZ), 'Failing, 2 in a row since 20 Oct, 11:10');
    assert.equal(trafficText(row({ traffic: { ...failing, state: 'down', consecutive_failures: 4 } }), NOW, TZ), 'Failing, 4 in a row since 20 Oct, 11:10');
    assert.equal(trafficText(row({ traffic: agedOut }), NOW, TZ), 'Last failed 2 days ago');
  });

  it('shows the failing time in the dashboard timezone', () => {
    assert.equal(trafficText(row({ traffic: failing }), NOW, 'Europe/Kyiv'), 'Failing, 2 in a row since 20 Oct, 14:10');
  });
});

describe('probeText', () => {
  it('shows a dash without a probe and Not run yet before the first check', () => {
    assert.equal(probeText(row({ has_probe: false, probe: signal() }), NOW), '—');
    assert.equal(probeText(row(), NOW), 'Not run yet');
  });

  it('words a passed and a failed check', () => {
    const failed = signal({ state: 'down', last_success_at: null, last_failure_at: '2026-10-20T11:30:00Z', consecutive_failures: 2, threshold: 1 });

    assert.equal(probeText(row({ probe: signal() }), NOW), 'Check OK 5 min ago');
    assert.equal(probeText(row({ probe: failed }), NOW), 'Check failed 30 min ago (2 in a row)');
    assert.equal(probeText(row({ probe: { ...failed, consecutive_failures: 1 } }), NOW), 'Check failed 30 min ago');
  });

  it('marks a check overdue only past 2 hours since its newest result', () => {
    const atLimit = signal({ last_success_at: '2026-10-20T10:00:00Z' });
    const overdue = signal({ last_success_at: '2026-10-20T09:00:00Z', last_failure_at: '2026-10-20T09:59:00Z' });

    assert.equal(probeText(row({ probe: atLimit }), NOW), 'Check OK 2 h ago');
    assert.equal(probeText(row({ probe: overdue }), NOW), 'Check OK 3 h ago, overdue');
  });
});

describe('errorShown', () => {
  it('is true only for a degraded or down row with a code on its failing signal', () => {
    const okProbe = signal();

    assert.equal(errorShown(row({ state: 'degraded', traffic: failing, probe: okProbe })), true);
    assert.equal(errorShown(row({ state: 'down', probe: { ...failing, state: 'down' } })), true);
    assert.equal(errorShown(row({ state: 'degraded', traffic: { ...failing, last_error_code: null } })), false);
    assert.equal(errorShown(row({ state: 'ok', traffic: signal({ last_error_code: 'parse', last_failure_at: '2026-10-20T10:00:00Z' }) })), false);
    assert.equal(errorShown(row({ state: 'stale', traffic: { ...failing, state: 'stale' } })), false);
    assert.equal(errorShown(row({ state: 'off', off_reason: 'budget_paused', traffic: failing })), false);
  });

  it('words the code of the failing signal', () => {
    const probe = signal({ state: 'down', last_failure_at: '2026-10-20T11:00:00Z', consecutive_failures: 3, last_error_code: 'http_429' });

    assert.equal(errorText(row({ state: 'down', traffic: failing, probe })), 'Rate limited (HTTP 429)');
    assert.equal(errorText(row({ state: 'degraded', traffic: failing })), 'Wrong content');
    assert.equal(errorText(row({ state: 'ok', traffic: signal() })), null);
  });
});

describe('ERROR_CODE_TEXT', () => {
  it('words every published code', () => {
    assert.deepEqual(ERROR_CODE_TEXT, {
      timeout: 'Timed out',
      http_4xx: 'Refused (HTTP 4xx)',
      http_5xx: 'Server error (HTTP 5xx)',
      http_429: 'Rate limited (HTTP 429)',
      transport: 'Connection failed',
      parse: 'Unreadable answer',
      wrong_content: 'Wrong content',
      not_configured: 'Not configured',
      other: 'Other error',
    });
  });
});

describe('servicesSubtitle', () => {
  it('counts the degraded and down rows, then explains fallback rows and yt-dlp', () => {
    const rows = [row({ state: 'down' }), row({ state: 'degraded' }), row({ state: 'stale' }), row({ state: 'off' })];

    assert.equal(servicesSubtitle(rows), `2 of 4 need a look. ${FALLBACK_NOTE}`);
    assert.equal(servicesSubtitle([row(), row({ state: 'idle' })]), `All services look fine. ${FALLBACK_NOTE}`);
    assert.match(FALLBACK_NOTE, /yt-dlp/);
  });
});

describe('ageText', () => {
  it('words the elapsed time in one unit', () => {
    assert.equal(ageText(null, NOW), '—');
    assert.equal(ageText('2026-10-20T11:59:30Z', NOW), 'just now');
    assert.equal(ageText('2026-10-20T11:48:00Z', NOW), '12 min ago');
    assert.equal(ageText('2026-10-19T12:00:00Z', NOW), '24 h ago');
    assert.equal(ageText('2026-10-17T12:00:00Z', NOW), '3 days ago');
  });
});

describe('minutesSince', () => {
  it('rounds and never goes below zero', () => {
    assert.equal(minutesSince('2026-10-20T11:56:40Z', NOW), 3);
    assert.equal(minutesSince('2026-10-20T12:05:00Z', NOW), 0);
  });
});

describe('values a newer bot publishes', () => {
  const newer = <T>(value: string): T => value as T;

  it('words an unknown row state as muted instead of failing to render', () => {
    assert.deepEqual(serviceBadge(row({ state: newer<ServiceState>('paused') }), NOW), { text: 'Unknown: paused', tone: 'muted' });
  });

  it('words an unknown signal state', () => {
    const traffic = signal({ state: newer<ServiceSignal['state']>('unreachable') });

    assert.equal(trafficText(row({ traffic }), NOW, TZ), 'Unknown: unreachable');
  });

  it('orders an unknown state after every known one', () => {
    const rows = [row({ key: 'new', state: newer<ServiceState>('paused') }), row({ key: 'off', state: 'off' })];
    const groups = groupServices(rows, PLATFORM_ORDER);

    assert.deepEqual(groups[0]?.rows.map((item) => item.key), ['off', 'new']);
  });

  it('falls back for an unknown off reason, error code and kind', () => {
    const reason = newer<ServiceOffReason>('quota_hit');
    const traffic = { ...failing, last_error_code: newer<ServiceSignal['last_error_code']>('captcha') };

    assert.equal(serviceBadge(row({ state: 'off', off_reason: reason }), NOW).text, 'Off: quota_hit');
    assert.equal(errorText(row({ state: 'degraded', traffic })), ERROR_CODE_TEXT.other);
    assert.equal(serviceKindLabel(newer<ServiceRow['kind']>('cdn')), 'cdn');
    assert.equal(serviceKindLabel('paid'), 'Paid API');
  });
});
