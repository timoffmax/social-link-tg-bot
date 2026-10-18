import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react';
import {
  ApiError,
  getReport,
  getReports,
  HTTP_NOT_FOUND,
  queueReportReply,
  REPORTS_LIMIT,
  REPORTS_WEEKS,
  setReportResolved,
  type Report,
  type ReportReply,
  type ReportReplyResponse,
  type ReportStatusFilter,
  type ReportWeek,
  type ReportsResponse,
} from '../api';
import { ClampedText, Segmented, StatTile, TileGroup, type SegmentOption } from '../components/Bits';
import { ColumnChart, SERIES_1, SERIES_2, type Series } from '../components/Charts';
import type { Column } from '../components/DataTable';
import { Panel } from '../components/Panel';
import { LengthCounter, Toolbar, useFormatTools } from '../components/TelegramEditing';
import { renderNodes, TelegramPreview } from '../components/TelegramPreview';
import { chatKind, chatLabel, formatClock, formatDay, formatNumber, formatRelative, formatTimestamp, formatWeek, isAllZero, userLabel } from '../format';
import { useApi, type Query } from '../hooks';
import {
  currentOverride,
  isPendingReply,
  REPLY_POLL_INTERVAL_MS,
  REPLY_POLL_MAX_MS,
  REPLY_STATUS_TEXT,
  shouldPollReply,
  UNKNOWN_DELIVERY_TEXT,
  type ReportOverride,
} from '../reports';
import { parseTelegramHtml, TELEGRAM_TEXT_LIMIT, type TgNode } from '../telegramHtml';
import type { TabProps } from './types';

const WEEKLY_SERIES: Series[] = [
  { key: 'open', label: 'Open', color: SERIES_1 },
  { key: 'resolved', label: 'Resolved', color: SERIES_2 },
];

const WEEKLY_COLUMNS: Column<ReportWeek>[] = [
  { header: 'Week', cell: (row) => formatWeek(row.week) },
  { header: 'Open', cell: (row) => formatNumber(row.open), numeric: true },
  { header: 'Resolved', cell: (row) => formatNumber(row.resolved), numeric: true },
];

const FILTER_OPTIONS: SegmentOption<ReportStatusFilter>[] = [
  { value: 'open', label: 'Open' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'all', label: 'All' },
];

const DEFAULT_FILTER: ReportStatusFilter = 'open';

const EMPTY_TEXT: Record<ReportStatusFilter, string> = {
  open: 'No open reports. Everything reported so far is resolved.',
  resolved: 'No resolved reports yet.',
  all: 'Nobody has reported a problem yet.',
};

const HTTP_BAD_REQUEST = 400;
const SUBMIT_KEY = 'Enter';

/** The counter's hint; the heading takes what the server's text limit leaves of Telegram's. */
function counterHint(replyTextLimit: number): string {
  const headingLength = TELEGRAM_TEXT_LIMIT - replyTextLimit;

  return `Characters Telegram counts (the text without tags, entities decoded). Telegram allows ${formatNumber(TELEGRAM_TEXT_LIMIT)}; the reply’s heading takes ${headingLength}.`;
}

type Overrides = Record<number, ReportOverride>;

/** One report's reply polling: its pending timer and the abort switch of its request in flight. */
interface ReplyPoll {
  timer: number | undefined;
  controller: AbortController;
}

function messageOf(error: unknown): string {
  const result = (error instanceof Error) ? error.message : 'Unknown error';

  return result;
}

function statusOf(error: unknown): number {
  const result = (error instanceof ApiError) ? error.status : 0;

  return result;
}

/** A failed write as one sentence for the card. */
function writeErrorText(error: unknown): string {
  const isGone = (statusOf(error) === HTTP_NOT_FOUND);
  const result = isGone ? 'This report no longer exists.' : `The server didn’t accept the change (${messageOf(error)}). Try again.`;

  return result;
}

export function ReportsTab({ tick, onLoaded }: TabProps) {
  const [filter, setFilter] = useState<ReportStatusFilter>(DEFAULT_FILTER);
  const query = useApi((signal) => getReports(REPORTS_LIMIT, REPORTS_WEEKS, filter, signal).then(onLoaded), [tick, filter]);
  const totals = query.data?.totals ?? null;

  return (
    <div className="grid">
      <TileGroup label="Report totals" loading={query.loading} error={(totals === null) ? query.error : null}>
        <StatTile label="Open reports" value={totals?.open ?? null} />
        <StatTile label="Resolved reports" value={totals?.resolved ?? null} />
      </TileGroup>
      <Weekly query={query} />
      <Latest query={query} filter={filter} onFilterChange={setFilter} />
    </div>
  );
}

function Weekly({ query }: { query: Query<ReportsResponse> }) {
  const rows = query.data?.weekly ?? [];

  return (
    <Panel
      title="Reports per week"
      subtitle={`Last ${REPORTS_WEEKS} weeks, by the week a report was sent and whether it is resolved now`}
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={isAllZero(rows, (row) => row.open + row.resolved)}
      emptyText={`No reports in the last ${REPORTS_WEEKS} weeks.`}
      table={{ columns: WEEKLY_COLUMNS, rows, rowKey: (row) => row.week }}
      wide
    >
      <ColumnChart data={rows} xKey="week" series={WEEKLY_SERIES} stacked formatTick={formatDay} formatLabel={formatWeek} />
    </Panel>
  );
}

/**
 * Reports changed on this page (a queued reply, a resolve, a poll) shown over the list on screen
 * until a newer list arrives, and the polling that follows a queued reply until the bot has sent it.
 */
function useReportOverrides(listedAt: string | null) {
  const [overrides, setOverrides] = useState<Overrides>({});
  const listedRef = useRef(listedAt);
  const polls = useRef(new Map<number, ReplyPoll>());
  const mounted = useRef(true);
  listedRef.current = listedAt;

  const store = useCallback((report: Report, generatedAt: string) => {
    const base = listedRef.current;

    if (base !== null) {
      setOverrides((current) => ({ ...current, [report.id]: { report, listedAt: base, generatedAt } }));
    }
  }, []);

  const stopPolling = useCallback((reportId: number) => {
    const poll = polls.current.get(reportId);

    if (poll !== undefined) {
      window.clearTimeout(poll.timer);
      poll.controller.abort();
      polls.current.delete(reportId);
    }
  }, []);

  // Each queued reply starts its own loop; a loop goes on only while the tab is mounted and no
  // newer loop has replaced it, so a request still in flight never revives a stopped loop.
  const follow = useCallback((reportId: number, replyId: number) => {
    const startedAt = Date.now();
    stopPolling(reportId);
    const loop: ReplyPoll = { timer: undefined, controller: new AbortController() };
    polls.current.set(reportId, loop);
    const isCurrent = () => (mounted.current && (polls.current.get(reportId) === loop));
    const schedule = () => {
      if (isCurrent()) {
        loop.timer = window.setTimeout(poll, REPLY_POLL_INTERVAL_MS);
      }
    };
    const poll = () => {
      getReport(reportId, loop.controller.signal)
        .then((response) => {
          const isFollowing = isCurrent();

          if (isFollowing) {
            store(response.report, response.generated_at);
          }

          if (isFollowing && shouldPollReply(response.report, replyId, startedAt, Date.now())) {
            schedule();
          }
        })
        .catch(() => {
          // A failed check (network, restart) is retried until the time is up.
          const hasTime = ((Date.now() - startedAt) < REPLY_POLL_MAX_MS);

          if (hasTime) {
            schedule();
          }
        });
    };
    schedule();
  }, [stopPolling, store]);

  useEffect(() => {
    const active = polls.current;
    mounted.current = true;

    return () => {
      mounted.current = false;

      for (const poll of active.values()) {
        window.clearTimeout(poll.timer);
        poll.controller.abort();
      }

      active.clear();
    };
  }, []);

  return { overrides, store, follow };
}

type ReportOverrides = ReturnType<typeof useReportOverrides>;

interface LatestProps {
  query: Query<ReportsResponse>;
  filter: ReportStatusFilter;
  onFilterChange: (filter: ReportStatusFilter) => void;
}

function Latest({ query, filter, onFilterChange }: LatestProps) {
  const data = query.data;
  const rows = data?.latest ?? [];
  const overrides = useReportOverrides(data?.generated_at ?? null);
  const [composing, setComposing] = useState<number | null>(null);

  return (
    <Panel
      title="Latest reports"
      subtitle={`Newest first, up to ${REPORTS_LIMIT}. Replies are sent by the bot to the reporter’s private chat.`}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={rows.length === 0}
      emptyText={EMPTY_TEXT[filter]}
      action={<Segmented label="Show reports" options={FILTER_OPTIONS} value={filter} onChange={onFilterChange} />}
      wide
    >
      {(data !== null) && (
        <ul className="reports">
          {rows.map((listed) => (
            <ReportItem
              key={listed.id}
              listed={listed}
              data={data}
              overrides={overrides}
              isComposing={(composing === listed.id)}
              onCompose={setComposing}
            />
          ))}
        </ul>
      )}
    </Panel>
  );
}

function reporterName(report: Report): string {
  const hasName = (report.first_name !== '');
  const handle = (report.username === '') ? '' : ` (@${report.username})`;
  const result = hasName ? `${report.first_name}${handle}` : userLabel(report.username, report.user_id);

  return result;
}

function whereSent(report: Report): string {
  const isGroup = (chatKind(report.chat_id) === 'group');
  const result = isGroup ? `in ${chatLabel(report.chat_title, report.chat_id)}` : 'in a private chat';

  return result;
}

interface ReportItemProps {
  listed: Report;
  data: ReportsResponse;
  overrides: ReportOverrides;
  isComposing: boolean;
  onCompose: (reportId: number | null) => void;
}

function ReportItem({ listed, data, overrides, isComposing, onCompose }: ReportItemProps) {
  const override = currentOverride(overrides.overrides[listed.id], data.generated_at);
  const report = override?.report ?? listed;
  const now = override?.generatedAt ?? data.generated_at;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const headingId = useId();

  const toggleResolved = () => {
    setBusy(true);
    setError(null);
    setReportResolved(report.id, (report.resolved === false))
      .then((response) => overrides.store(response.report, response.generated_at))
      .catch((failure: unknown) => setError(writeErrorText(failure)))
      .finally(() => setBusy(false));
  };

  const onQueued = (response: ReportReplyResponse) => {
    overrides.store(response.report, response.generated_at);
    overrides.follow(report.id, response.reply.id);
    onCompose(null);
  };

  return (
    <li className={report.resolved ? 'report report--resolved' : 'report'} aria-labelledby={headingId}>
      <div className="report__head">
        <span className={report.resolved ? 'report__state report__state--resolved' : 'report__state'}>
          <span aria-hidden="true">{report.resolved ? '✓' : '●'}</span>
          {report.resolved ? 'Resolved' : 'Open'}
        </span>
        <span className="report__id">#{report.id}</span>
        <time dateTime={report.created_at ?? undefined} title={formatTimestamp(report.created_at, data.timezone)}>
          {formatRelative(report.created_at, now)}
        </time>
      </div>
      <div id={headingId} className="report__who">{reporterName(report)} {whereSent(report)}</div>
      <ClampedText text={report.message} />
      {(report.replies.length > 0) && <ReplyList replies={report.replies} now={now} timezone={data.timezone} />}
      {report.resolved && <ResolvedLine report={report} now={now} timezone={data.timezone} />}
      {(isComposing === false) && (
        <div className="report__actions">
          <button type="button" className="button button--small" onClick={() => onCompose(report.id)}>Reply</button>
          <button type="button" className="button button--small button--quiet" onClick={toggleResolved} disabled={busy}>
            {busy ? 'Saving…' : (report.resolved ? 'Reopen' : 'Resolve')}
          </button>
        </div>
      )}
      {(error !== null) && <div className="state state--error report__error" role="alert">{error}</div>}
      {isComposing && (
        <ReplyComposer
          report={report}
          timezone={data.timezone}
          replyPrefix={data.reply_prefix}
          replyTextLimit={data.reply_text_limit}
          onQueued={onQueued}
          onCancel={() => onCompose(null)}
        />
      )}
    </li>
  );
}

function ResolvedLine({ report, now, timezone }: { report: Report; now: string; timezone: string }) {
  const resolvedAt = report.resolved_at;

  return (
    <p className="report__resolved">
      {(resolvedAt === null) ? 'Resolved before resolve times were recorded' : (
        <>Resolved <time dateTime={resolvedAt} title={formatTimestamp(resolvedAt, timezone)}>{formatRelative(resolvedAt, now)}</time></>
      )}
    </p>
  );
}

const SOURCE_TEXT: Record<ReportReply['source'], string> = {
  dashboard: 'from the dashboard',
  telegram: 'with /reply in Telegram',
};

function ReplyBadge({ reply }: { reply: ReportReply }) {
  const text = REPLY_STATUS_TEXT[reply.status];

  return (
    <span className={`badge badge--reply-${reply.status}`} title={text.hint}>
      <span aria-hidden="true" className="badge__icon">{text.icon}</span>
      {text.label}
    </span>
  );
}

/** The reply as Telegram shows it; text that does not parse (never expected) is shown as typed. */
function ReplyText({ text }: { text: string }) {
  const parsed = useMemo(() => parseTelegramHtml(text), [text]);
  const isFormatted = (parsed.problem === null);

  return (
    <div className="reply__text">{isFormatted ? renderNodes(parsed.nodes) : text}</div>
  );
}

function ReplyList({ replies, now, timezone }: { replies: ReportReply[]; now: string; timezone: string }) {
  return (
    <ol className="replies" aria-label="Replies">
      {replies.map((reply) => (
        <li key={reply.id} className={`reply reply--${reply.status}`}>
          <div className="reply__meta">
            <ReplyBadge reply={reply} />
            <span>{SOURCE_TEXT[reply.source]}</span>
            <time dateTime={reply.created_at ?? undefined} title={formatTimestamp(reply.created_at, timezone)}>
              {formatRelative(reply.created_at, now)}
            </time>
          </div>
          <ReplyText text={reply.text} />
          <ReplyOutcome reply={reply} />
        </li>
      ))}
    </ol>
  );
}

/** What became of a reply beyond its badge: the reason it failed, why it may have been delivered, a retry. */
function ReplyOutcome({ reply }: { reply: ReportReply }) {
  const isPending = isPendingReply(reply);
  const isRetrying = (reply.status === 'queued') && (reply.error !== null);
  const isResolving = isPending && reply.resolve_on_send;
  const reason = (reply.error === null) ? '' : ` (${reply.error})`;

  return (
    <>
      {(reply.status === 'unknown') && <p className="reply__error reply__error--unknown">{UNKNOWN_DELIVERY_TEXT}{reason}</p>}
      {(reply.status === 'failed') && (reply.error !== null) && <p className="reply__error">Not sent: {reply.error}</p>}
      {isRetrying && <p className="reply__note">Telegram asked to wait, the bot tries again{reason}</p>}
      {isResolving && <p className="reply__note">Resolves the report once delivered</p>}
    </>
  );
}

interface ReplyComposerProps {
  report: Report;
  timezone: string;
  /** The heading the bot puts above the reply, from the server. */
  replyPrefix: string;
  /** The server's limit for the reply text. */
  replyTextLimit: number;
  onQueued: (response: ReportReplyResponse) => void;
  onCancel: () => void;
}

function ReplyComposer({ report, timezone, replyPrefix, replyTextLimit, onQueued, onCancel }: ReplyComposerProps) {
  const [text, setText] = useState('');
  const [resolve, setResolve] = useState(true);
  const [sending, setSending] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const textId = useId();
  const resolveId = useId();
  const parsed = useMemo(() => parseTelegramHtml(text), [text]);
  const message = useMemo(() => parseTelegramHtml(`${replyPrefix}${text.trim()}`), [replyPrefix, text]);
  const lastGood = useRef<TgNode[]>(message.nodes);
  const problem = parsed.problem;
  const isOver = (problem === null) && (parsed.length > replyTextLimit);
  const isEmpty = (text.trim() === '');
  const canSend = (isEmpty === false) && (problem === null) && (isOver === false) && (sending === false);
  const tools = useFormatTools(textarea, text, setText, sending);
  const describedBy = `${textId}-problems`;

  if (message.problem === null) {
    lastGood.current = message.nodes;
  }

  useEffect(() => {
    textarea.current?.focus();
  }, []);

  const edit = (next: string) => {
    setText(next);
    setFieldError(null);
  };

  const send = () => {
    if (canSend) {
      setSending(true);
      setFormError(null);
      queueReportReply(report.id, { text, resolve })
        .then(onQueued)
        .catch((error: unknown) => {
          const isInvalid = (statusOf(error) === HTTP_BAD_REQUEST);

          if (isInvalid) {
            setFieldError(messageOf(error));
          }

          if (isInvalid === false) {
            setFormError(writeErrorText(error));
          }

          setSending(false);
        });
    }
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    send();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    const isSubmit = (event.ctrlKey || event.metaKey) && (event.key === SUBMIT_KEY);

    if (isSubmit) {
      event.preventDefault();
      send();
    }

    if (isSubmit === false) {
      tools.onKeyDown(event);
    }
  };

  return (
    <form className="composer" onSubmit={onSubmit} noValidate aria-label={`Reply to report #${report.id}`}>
      <div className="editor__columns">
        <div className="field">
          <div className="field__label-row">
            <label className="field__label" htmlFor={textId}>Reply to {reporterName(report)}</label>
            <LengthCounter parsed={parsed} limit={replyTextLimit} hint={counterHint(replyTextLimit)} />
          </div>
          <Toolbar controls={textId} disabled={sending} onApply={tools.applyTool} />
          <textarea
            id={textId}
            ref={textarea}
            className="input input--code composer__text"
            rows={6}
            value={text}
            onChange={(event) => edit(event.target.value)}
            onKeyDown={onKeyDown}
            readOnly={sending}
            aria-invalid={(problem !== null) || isOver || (fieldError !== null)}
            aria-describedby={describedBy}
            spellCheck
          />
          <div id={describedBy}>
            {(problem !== null) && (
              <p className="field__error" role="alert">
                Telegram would reject this. Line {problem.line}, column {problem.column}: {problem.message}.
              </p>
            )}
            {isOver && (
              <p className="field__error">
                Replies can have {formatNumber(replyTextLimit)} characters; this one is {formatNumber(parsed.length - replyTextLimit)} over.
              </p>
            )}
            {(fieldError !== null) && <p className="field__error" role="alert">Not sent: {fieldError}</p>}
          </div>
        </div>
        <div className="field">
          <div className="field__label-row">
            <span className="field__label">Preview</span>
            <span className="field__hint">What the reporter receives from the bot</span>
          </div>
          <TelegramPreview
            nodes={(message.problem === null) ? message.nodes : lastGood.current}
            stale={(message.problem !== null)}
            time={formatClock(new Date().toISOString(), timezone)}
          />
        </div>
      </div>

      {(formError !== null) && <div className="state state--error" role="alert">{formError}</div>}

      <div className="composer__actions">
        <label className="checkbox" htmlFor={resolveId}>
          <input id={resolveId} type="checkbox" checked={resolve} onChange={(event) => setResolve(event.target.checked)} disabled={sending} />
          Resolve after it’s delivered
        </label>
        <div className="composer__buttons">
          <button type="submit" className="button button--primary" disabled={(canSend === false)}>
            {sending ? 'Sending…' : 'Send reply'}
          </button>
          <button type="button" className="button button--quiet" onClick={onCancel} disabled={sending}>Cancel</button>
        </div>
      </div>
      <p className="editor__note">
        The bot sends the reply within a few seconds to the reporter’s private chat, below the heading shown in the preview,
        and resolves the report only once Telegram confirms the delivery. If the reporter has blocked the bot, it fails and
        the reason shows here.
      </p>
    </form>
  );
}
