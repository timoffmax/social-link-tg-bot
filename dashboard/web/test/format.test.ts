import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { formatRelative, formatRelativeShort, NO_TIMESTAMP, tickIntervalForWidth } from '../src/format.ts';

const NOW = '2026-10-18T12:00:00Z';

describe('formatRelativeShort', () => {
  it('formats the elapsed time in one unit', () => {
    assert.equal(formatRelativeShort('2026-10-18T11:59:30Z', NOW), 'now');
    assert.equal(formatRelativeShort('2026-10-18T11:55:00Z', NOW), '5m');
    assert.equal(formatRelativeShort('2026-10-18T06:00:00Z', NOW), '6h');
    assert.equal(formatRelativeShort('2026-10-15T12:00:00Z', NOW), '3d');
  });

  it('picks the unit after rounding at the unit boundaries', () => {
    assert.equal(formatRelativeShort('2026-10-18T11:59:01Z', NOW), 'now');
    assert.equal(formatRelativeShort('2026-10-18T11:59:00Z', NOW), '1m');
    assert.equal(formatRelativeShort('2026-10-18T11:00:31Z', NOW), '59m');
    assert.equal(formatRelativeShort('2026-10-18T11:00:20Z', NOW), '1h');
    assert.equal(formatRelativeShort('2026-10-16T12:31:00Z', NOW), '47h');
    assert.equal(formatRelativeShort('2026-10-16T12:20:00Z', NOW), '2d');
    assert.equal(formatRelativeShort('2026-10-16T12:00:00Z', NOW), '2d');
  });

  it('shows a dash for a missing timestamp', () => {
    assert.equal(formatRelativeShort(null, NOW), NO_TIMESTAMP);
  });

  it('shows a future timestamp as now', () => {
    assert.equal(formatRelativeShort('2026-10-18T12:05:00Z', NOW), 'now');
  });
});

describe('formatRelative', () => {
  // The wording comes from the runtime's ICU data; the test checks the unit and count.
  const relative = new Intl.RelativeTimeFormat('en-GB', { numeric: 'auto', style: 'short' });

  it('uses the same unit as the short form', () => {
    assert.equal(formatRelative('2026-10-18T11:59:30Z', NOW), 'just now');
    assert.equal(formatRelative('2026-10-18T11:55:00Z', NOW), relative.format(-5, 'minute'));
    assert.equal(formatRelative('2026-10-18T11:00:20Z', NOW), relative.format(-1, 'hour'));
    assert.equal(formatRelative('2026-10-16T12:31:00Z', NOW), relative.format(-47, 'hour'));
    assert.equal(formatRelative('2026-10-16T12:20:00Z', NOW), relative.format(-2, 'day'));
  });

  it('shows a dash for a missing timestamp', () => {
    assert.equal(formatRelative(null, NOW), NO_TIMESTAMP);
  });
});

describe('tickIntervalForWidth', () => {
  const LABEL = 46;
  const GAP = 12;

  for (const count of [7, 30, 90, 180, 365]) {
    for (const width of [240, 280, 320, 360, 1100]) {
      it(`keeps ${count} labels apart on a ${width}px axis`, () => {
        const step = tickIntervalForWidth(count, width, LABEL, GAP, 7) + 1;
        const spacing = (step * width) / count;

        assert.ok(spacing >= (LABEL + GAP), `spacing ${spacing}`);
        assert.ok(Math.ceil(count / step) <= 7);
      });
    }
  }

  it('falls back to the tick cap before the width is known', () => {
    assert.equal(tickIntervalForWidth(90, 0, LABEL, GAP, 7), 12);
  });
});
