// Pure helpers for report replies (type-only imports, so `node --test` runs them as is).
import type { ReplyStatus, Report, ReportReply } from './api';

/** The bot's outbox looks for queued replies every 5 seconds; the page checks as often. */
export const REPLY_POLL_INTERVAL_MS = 5_000;

/** Polling stops after two minutes; the next refresh still shows the outcome. */
export const REPLY_POLL_MAX_MS = 120_000;

const PENDING_STATUSES: ReplyStatus[] = ['queued', 'sending'];

/** Still waiting for the bot: queued (maybe for another attempt), or claimed and being sent. */
export function isPendingReply(reply: ReportReply): boolean {
  return PENDING_STATUSES.includes(reply.status);
}

/** Whether to check the report again: the reply is still pending (or not visible yet) and time is left. */
export function shouldPollReply(report: Report, replyId: number, startedAt: number, now: number): boolean {
  const reply = report.replies.find((candidate) => (candidate.id === replyId));
  const isWaiting = (reply === undefined) || isPendingReply(reply);
  const hasTime = ((now - startedAt) < REPLY_POLL_MAX_MS);

  return isWaiting && hasTime;
}

/** What an unknown reply means for the owner. */
export const UNKNOWN_DELIVERY_TEXT = 'May have been delivered — check before sending again';

export interface ReplyStatusText {
  icon: string;
  label: string;
  hint: string;
}

export const REPLY_STATUS_TEXT: Record<ReplyStatus, ReplyStatusText> = {
  queued: { icon: '…', label: 'Queued', hint: 'Waiting for the bot to send it, usually within a few seconds' },
  sending: { icon: '…', label: 'Sending', hint: 'The bot is sending it now' },
  sent: { icon: '✓', label: 'Sent', hint: 'Delivered to the reporter in Telegram' },
  failed: { icon: '✕', label: 'Failed', hint: 'Not delivered: Telegram refused it or it never left the bot; it is not sent again automatically' },
  unknown: { icon: '?', label: 'Delivery unknown', hint: UNKNOWN_DELIVERY_TEXT },
};

/** A report fetched by a write or a poll after the list it belongs to was loaded. */
export interface ReportOverride {
  report: Report;
  /** generated_at of the list it overrides: a newer list replaces it. */
  listedAt: string;
  /** generated_at of the answer that carried it, for relative times. */
  generatedAt: string;
}

/**
 * The override to show for a listed report: only one made against the list on screen, so a reply
 * queued here or a resolve stays visible until the next refresh brings a newer list.
 */
export function currentOverride(override: ReportOverride | undefined, listedAt: string): ReportOverride | null {
  const isCurrent = (override !== undefined) && (override.listedAt === listedAt);
  const result = isCurrent ? override : null;

  return result;
}
