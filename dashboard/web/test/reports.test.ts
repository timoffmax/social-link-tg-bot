import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { Report, ReportReply } from '../src/api.ts';
import {
  currentOverride,
  isPendingReply,
  REPLY_POLL_MAX_MS,
  REPLY_STATUS_TEXT,
  shouldPollReply,
  UNKNOWN_DELIVERY_TEXT,
} from '../src/reports.ts';

function reply(id: number, status: ReportReply['status']): ReportReply {
  return {
    id,
    text: 't',
    source: 'dashboard',
    status,
    resolve_on_send: false,
    attempts: 0,
    next_attempt_at: null,
    error: null,
    created_at: null,
    sent_at: null,
  };
}

function report(replies: ReportReply[]): Report {
  return {
    id: 1,
    created_at: null,
    user_id: 42,
    username: '',
    first_name: '',
    chat_id: 42,
    chat_title: '',
    message: 'm',
    resolved: false,
    resolved_at: null,
    replies,
  };
}

describe('REPLY_STATUS_TEXT', () => {
  it('says an unknown reply may have been delivered', () => {
    assert.equal(REPLY_STATUS_TEXT.unknown.hint, UNKNOWN_DELIVERY_TEXT);
    assert.equal(UNKNOWN_DELIVERY_TEXT, 'May have been delivered — check before sending again');
  });
});

describe('isPendingReply', () => {
  it('is pending while queued or sending', () => {
    assert.equal(isPendingReply(reply(1, 'queued')), true);
    assert.equal(isPendingReply(reply(1, 'sending')), true);
    assert.equal(isPendingReply(reply(1, 'sent')), false);
    assert.equal(isPendingReply(reply(1, 'failed')), false);
    assert.equal(isPendingReply(reply(1, 'unknown')), false);
  });
});

describe('shouldPollReply', () => {
  it('polls while the reply is pending and time is left', () => {
    assert.equal(shouldPollReply(report([reply(5, 'queued')]), 5, 0, 1_000), true);
    assert.equal(shouldPollReply(report([reply(5, 'sending')]), 5, 0, REPLY_POLL_MAX_MS - 1), true);
  });

  it('stops when the reply is sent, failed or unknown', () => {
    assert.equal(shouldPollReply(report([reply(4, 'queued'), reply(5, 'sent')]), 5, 0, 1_000), false);
    assert.equal(shouldPollReply(report([reply(5, 'failed')]), 5, 0, 1_000), false);
    assert.equal(shouldPollReply(report([reply(5, 'unknown')]), 5, 0, 1_000), false);
  });

  it('stops after two minutes', () => {
    assert.equal(shouldPollReply(report([reply(5, 'queued')]), 5, 0, REPLY_POLL_MAX_MS), false);
  });

  it('keeps polling for a reply not visible yet', () => {
    assert.equal(shouldPollReply(report([]), 5, 0, 1_000), true);
  });
});

describe('currentOverride', () => {
  it('applies only to the list it was made against', () => {
    const override = { report: report([]), listedAt: '2026-10-18T12:00:00Z', generatedAt: '2026-10-18T12:00:05Z' };

    assert.equal(currentOverride(override, '2026-10-18T12:00:00Z'), override);
    assert.equal(currentOverride(override, '2026-10-18T12:05:00Z'), null);
    assert.equal(currentOverride(undefined, '2026-10-18T12:00:00Z'), null);
  });
});
