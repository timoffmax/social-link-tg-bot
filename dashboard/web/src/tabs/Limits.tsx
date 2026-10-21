import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react';
import {
  ApiError,
  getLimits,
  getLimitScope,
  queueLimitChange,
  type ApifyService,
  type HikerService,
  type LimitDefaults,
  type LimitRow,
  type LimitScope,
  type LimitsResponse,
  type PublishedComponent,
  type RelayStatus,
  type SpecialLimitRules,
} from '../api';
import { Meter, Segmented, StaleNotice, StateBadge, StatTile, TileGroup, type SegmentOption } from '../components/Bits';
import { DataTable, type Column } from '../components/DataTable';
import { Panel } from '../components/Panel';
import { RequestStatus, useRequestFollow } from '../components/RequestFollow';
import { formatNumber, formatTimestamp, platformLabel } from '../format';
import { useApi, useVisibleTick, type Query } from '../hooks';
import {
  accountLimitText,
  apifySpendText,
  breakerText,
  canChange,
  ceilingNote,
  fill,
  formatBytes,
  formatDuration,
  formatRate,
  formatResetDate,
  formatUsd,
  globalCapNote,
  GROUP_DISABLED_NOTE,
  hikerSpendText,
  ID_SIGN_HINT,
  limitConfirm,
  limitErrorText,
  LIMITS_REFRESH_MS,
  limitText,
  matchesFilter,
  NOT_SUPPORTED_TEXT,
  paidStateText,
  paidTone,
  parseLimitInput,
  parseScopeId,
  pausedFeatureLines,
  quickTargets,
  relayCardState,
  relayHistoryByPlatform,
  rowName,
  STALE_BOT_TEXT,
  stateLabel,
  limitChangeText,
  limitTargetKey,
  TRUNCATED_TEXT,
  UNLIMITED,
  unlimitedSwitch,
  usageAge,
  type RelayPlatformHistory,
  type RelayWindowStats,
} from '../limits';
import { actorLabel, clockOffset, isOpenStatus, serverNow, shouldPollRequest } from '../settings';
import type { TabProps } from './types';

type ScopeFilter = 'all' | LimitScope;

const SCOPE_OPTIONS: SegmentOption<ScopeFilter>[] = [
  { value: 'all', label: 'All' },
  { value: 'user', label: 'People' },
  { value: 'chat', label: 'Groups' },
];

const FIND_OPTIONS: SegmentOption<LimitScope>[] = [
  { value: 'user', label: 'Person' },
  { value: 'chat', label: 'Group' },
];

const PROVIDER_LABELS: Record<string, string> = { apify: 'Apify', hiker: 'HikerAPI' };
const PROVIDERS = ['apify', 'hiker'];

/** What every row needs from the page. */
interface LimitsContext {
  applying: boolean;
  rules: SpecialLimitRules | null;
  defaults: LimitDefaults;
  resets: string;
  timezone: string;
  /** Server clock minus browser clock, from the last load. */
  offset: number;
  /** A followed change is decided or expired: reload the tab. */
  onFinal: () => void;
}

function messageOf(error: unknown): string {
  return (error instanceof Error) ? error.message : 'Unknown error';
}

function statusOf(error: unknown): number {
  return (error instanceof ApiError) ? error.status : 0;
}

function isFromDashboard(error: unknown): boolean {
  return (error instanceof ApiError) ? error.isFromDashboard : false;
}

export function LimitsTab({ tick, onLoaded }: TabProps) {
  const [localTick, reload] = useVisibleTick(LIMITS_REFRESH_MS);
  const query = useApi((signal) => getLimits(signal).then(onLoaded), [tick, localTick]);
  const data = query.data;
  const offset = useMemo(() => ((data === null) ? 0 : clockOffset(data.generated_at)), [data]);
  const context: LimitsContext | null = (data === null) ? null : {
    applying: data.applying,
    rules: data.rules,
    defaults: data.defaults,
    resets: formatResetDate(data.resets_at),
    timezone: data.timezone,
    offset,
    onFinal: reload,
  };

  return (
    <div className="grid">
      {(data !== null) && <LimitsBanners data={data} />}
      <PaidServicesPanel query={query} offset={offset} />
      <RelayPanel query={query} offset={offset} />
      <LimitTiles query={query} />
      <LimitsPanel query={query} context={context} refreshKey={`${tick}-${localTick}`} />
    </div>
  );
}

function LimitsBanners({ data }: { data: LimitsResponse }) {
  return (
    <>
      {(data.applying === false) && (
        <div className="notice notice--warning panel--wide" role="status"><p>{STALE_BOT_TEXT}</p></div>
      )}
      {(data.rules === null) && (
        <div className="notice notice--warning panel--wide" role="status"><p>{NOT_SUPPORTED_TEXT}</p></div>
      )}
    </>
  );
}

function PaidServicesPanel({ query, offset }: { query: Query<LimitsResponse>; offset: number }) {
  const paid = query.data?.paid ?? null;
  const data = paid?.data ?? null;
  const order = data?.order ?? [];
  const providers = [...PROVIDERS].sort((left, right) => rankOf(order, left) - rankOf(order, right));
  const orderText = order.map((name) => PROVIDER_LABELS[name] ?? name).join(' → ');
  const now = serverNow(offset);

  return (
    <Panel
      title="Paid services"
      subtitle="What Apify and HikerAPI may still spend; a paused service is skipped and the next one is tried. Budgets change in Settings (a raise needs your Approve in Telegram)."
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={paid === null}
      emptyText="Waiting for the bot to report its paid services (every minute once updated)."
    >
      {(paid !== null) && paid.is_stale && <StaleNotice updatedAt={paid.updated_at} now={now} />}
      {(data !== null) && (
        <>
          {(orderText !== '') && <p className="paid-order">Tried in order: {orderText}</p>}
          <div className="paid-cards">
            {providers.map((name) => ((name === 'apify')
              ? <ApifyCard key={name} service={data.apify} now={now} />
              : <HikerCard key={name} service={data.hiker} now={now} />))}
          </div>
          <p className="panel__foot"><a href="#/settings">Change a budget</a></p>
        </>
      )}
    </Panel>
  );
}

/** A provider's place in the order; one not listed comes last. */
function rankOf(order: string[], name: string): number {
  const index = order.indexOf(name);

  return (index === -1) ? order.length : index;
}

function ApifyCard({ service, now }: { service: ApifyService; now: number }) {
  const accountLimit = accountLimitText(service);
  const pausedLines = pausedFeatureLines(service);

  return (
    <article className="paid-card" aria-label="Apify">
      <header className="paid-card__head">
        <h3 className="paid-card__name">Apify</h3>
        <StateBadge tone={paidTone(service)} text={paidStateText(service)} />
      </header>
      <Meter used={service.used_usd ?? 0} limit={service.budget_usd} />
      <p className="paid-card__line">{apifySpendText(service)}</p>
      {(service.in_flight_usd > 0) && <p className="paid-card__line">+{formatUsd(service.in_flight_usd)} in flight</p>}
      {pausedLines.map((line) => <p key={line} className="paid-card__line paid-card__line--warning">{line}</p>)}
      {(accountLimit !== null) && <p className="paid-card__line paid-card__line--warning">{accountLimit}</p>}
      <p className="paid-card__line paid-card__line--muted">Usage {usageAge(service.usage_read_at, now)}</p>
      {((service.usage_error ?? '') !== '') && (
        <details className="paid-card__details">
          <summary>Last usage read failed</summary>
          <p>{service.usage_error}</p>
        </details>
      )}
    </article>
  );
}

function HikerCard({ service, now }: { service: HikerService; now: number }) {
  const balance = service.balance_requests;

  return (
    <article className="paid-card" aria-label="HikerAPI">
      <header className="paid-card__head">
        <h3 className="paid-card__name">HikerAPI</h3>
        <StateBadge tone={paidTone(service)} text={paidStateText(service)} />
      </header>
      <Meter used={service.spent_usd ?? 0} limit={service.budget_usd} />
      <p className="paid-card__line">{hikerSpendText(service)}</p>
      {(service.in_flight_usd > 0) && <p className="paid-card__line">+{formatUsd(service.in_flight_usd)} in flight</p>}
      {(balance !== null) && (
        <p className="paid-card__line paid-card__line--muted">
          Balance: {formatNumber(balance)} requests left ({usageAge(service.balance_read_at, now)})
        </p>
      )}
    </article>
  );
}

function RelayPanel({ query, offset }: { query: Query<LimitsResponse>; offset: number }) {
  const relay = query.data?.relay ?? null;
  const isBotStale = (query.data?.bot?.is_stale === true);
  const now = serverNow(offset);
  const state = relayCardState(relay, isBotStale, now);
  const data = relay?.data ?? null;
  const isShown = (data !== null) && (state.kind !== 'not_configured');
  const emptyText = (state.kind === 'not_configured') ? 'The home relay is not configured.' : 'Waiting for the bot to report the relay (every minute once updated).';

  return (
    <Panel
      title="Home relay (NAS)"
      subtitle="The home machine that fetches videos from a residential IP; the bot checks it every minute."
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={(isShown === false)}
      emptyText={emptyText}
    >
      {isShown && (relay !== null) && <RelayBody relay={relay} state={state} now={now} timezone={query.data?.timezone ?? 'UTC'} />}
    </Panel>
  );
}

interface RelayBodyProps {
  relay: PublishedComponent<RelayStatus>;
  state: ReturnType<typeof relayCardState>;
  now: number;
  timezone: string;
}

function RelayBody({ relay, state, now, timezone }: RelayBodyProps) {
  const data = relay.data;
  const platforms = Object.entries(data.platforms ?? {});
  const checked = (data.checked_at === null) ? 'never checked' : `checked ${usageAge(data.checked_at, now).replace(/^read /, '')}`;

  return (
    <div className="relay">
      <p className="relay__head">
        <StateBadge tone={state.tone} text={state.label} />
        <span className="relay__meta">
          <time dateTime={data.checked_at ?? undefined} title={formatTimestamp(data.checked_at, timezone)}>{checked}</time>
          {(data.yt_dlp !== '') && <> · yt-dlp {data.yt_dlp}</>}
        </span>
      </p>
      {(state.kind === 'unreachable') && (data.error !== '') && <p className="relay__error">{data.error}</p>}
      <dl className="relay-counters">
        <RelayCounter label="All platforms this hour" used={data.global.hour} limit={data.global.hourly_limit} />
        <RelayCounter label="All platforms today" used={data.global.day} limit={data.global.daily_limit} />
      </dl>
      {(platforms.length > 0) && (
        <ul className="relay-platforms">
          {platforms.map(([name, platform]) => (
            <li key={name} className="relay-platform">
              <div className="relay-platform__head">
                <strong>{platformLabel(name)}</strong>
                <span className={platform.enabled ? 'chip' : 'chip chip--off'}>{platform.enabled ? 'On' : 'Off'}</span>
                <span className={(platform.breaker === 'open') ? 'chip chip--off' : 'chip'}>Breaker {breakerText(platform)}</span>
              </div>
              <dl className="relay-counters">
                <RelayCounter label="This hour" used={platform.hour} limit={platform.hourly_limit} />
                <RelayCounter label="Today" used={platform.day} limit={platform.daily_limit} />
              </dl>
            </li>
          ))}
        </ul>
      )}
      <RelayHistoryTable relay={data} />
    </div>
  );
}

function RelayCounter({ label, used, limit }: { label: string; used: number; limit: number }) {
  return (
    <div className="relay-counter">
      <dt>{label}</dt>
      <dd>
        <span className="used">
          <Meter used={used} limit={(limit > 0) ? limit : null} />
          {formatNumber(used)}{(limit > 0) ? ` of ${formatNumber(limit)}` : ''}
        </span>
      </dd>
    </div>
  );
}

interface HistoryLine {
  platform: string;
  window: string;
  stats: RelayWindowStats;
}

function failureText(stats: RelayWindowStats): string {
  const parts = stats.failures.map((failure) => `${failure.code} ${formatNumber(failure.count)}`);

  return (parts.length === 0) ? '—' : parts.join(', ');
}

const HISTORY_COLUMNS: Column<HistoryLine>[] = [
  { header: 'Platform', cell: (line) => platformLabel(line.platform), fill: true },
  { header: 'Window', cell: (line) => line.window },
  { header: 'OK', cell: (line) => formatNumber(line.stats.ok), numeric: true },
  { header: 'Failed', cell: (line) => formatNumber(line.stats.failed), numeric: true },
  { header: 'Success', cell: (line) => formatRate(line.stats.rate), numeric: true },
  { header: 'Failures by code', cell: (line) => failureText(line.stats), detail: true },
];

function historyLines(history: RelayPlatformHistory[]): HistoryLine[] {
  return history.flatMap((platform) => (['24h', '7d', '30d'] as const).map((window) => ({ platform: platform.platform, window, stats: platform.windows[window] })));
}

function RelayHistoryTable({ relay }: { relay: RelayStatus }) {
  const history = relay.history;
  const byPlatform = (history === null) ? [] : relayHistoryByPlatform(history);
  const lines = historyLines(byPlatform);

  return (
    <section className="relay-history" aria-label="Relay history">
      <h3 className="relay-history__title">History</h3>
      {(history === null) && <p className="relay-history__empty">No history: the bot has no database.</p>}
      {(history !== null) && (lines.length === 0) && <p className="relay-history__empty">No relay calls in the last 30 days.</p>}
      {(lines.length > 0) && <DataTable columns={HISTORY_COLUMNS} rows={lines} rowKey={(line) => `${line.platform}-${line.window}`} caption="Relay calls by platform" />}
      {(history !== null) && (
        <p className="relay-history__foot">
          Data served: {formatBytes(history.ok_bytes['24h'])} in 24 h, {formatBytes(history.ok_bytes['7d'])} in 7 d · average
          {' '}{formatDuration(history.ok_avg_ms['24h'])} (24 h), {formatDuration(history.ok_avg_ms['7d'])} (7 d)
        </p>
      )}
    </section>
  );
}

function LimitTiles({ query }: { query: Query<LimitsResponse> }) {
  const data = query.data;
  const global = data?.global ?? null;
  const capNote = (global === null) ? undefined : globalCapNote(global);

  return (
    <TileGroup label="Special downloads this month" loading={query.loading} error={(data === null) ? query.error : null}>
      <StatTile label="Special downloads this month, bot-wide" value={global?.used ?? null} note={capNote} />
      <StatTile label="Default per person" value={data?.defaults.user ?? null} note="private chats" />
      <StatTile label="Default per group" value={data?.defaults.chat ?? null} note="the group counts, not the member" />
      <StatTile label="Resets" value={null} display={(data === null) ? '–' : formatResetDate(data.resets_at)} note="UTC" />
    </TileGroup>
  );
}

interface LimitsPanelProps {
  query: Query<LimitsResponse>;
  context: LimitsContext | null;
  /** Changes on every reload, so a found row is read again. */
  refreshKey: string;
}

function LimitsPanel({ query, context, refreshKey }: LimitsPanelProps) {
  const [scope, setScope] = useState<ScopeFilter>('all');
  const [search, setSearch] = useState('');
  const [isFinding, setFinding] = useState(false);
  const [found, setFound] = useState<{ scope: LimitScope; id: number } | null>(null);
  const searchId = useId();
  const data = query.data;
  const rows = (data?.rows ?? []).filter((row) => matchesFilter(row, scope, search));
  const truncated = (data !== null) && data.truncated ? fill(TRUNCATED_TEXT, { shown: formatNumber(data.rows.length), total: formatNumber(data.total) }) : null;

  const actions = (
    <div className="limits-filter">
      <Segmented label="Show" options={SCOPE_OPTIONS} value={scope} onChange={setScope} />
      <label className="visually-hidden" htmlFor={searchId}>Search by name, @username or id</label>
      <input
        id={searchId}
        type="search"
        className="input input--compact"
        placeholder="Name, @username or id"
        value={search}
        onChange={(event) => setSearch(event.target.value)}
      />
      <button type="button" className="button button--small" aria-expanded={isFinding} onClick={() => setFinding((value) => (value === false))}>
        Find by id
      </button>
    </div>
  );

  return (
    <Panel
      title="People and groups"
      subtitle="This month’s special downloads against each limit, nearest the limit first. In groups the group is counted, not the member."
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={false}
      emptyText=""
      action={actions}
      wide
    >
      {isFinding && <FindLimit onFind={setFound} />}
      {(found !== null) && (context !== null) && <FoundLimit key={limitTargetKey(found.scope, found.id)} target={found} context={context} refreshKey={refreshKey} />}
      {(truncated !== null) && <p className="limits-note">{truncated}</p>}
      {(rows.length === 0) && (
        <div className="state state--empty">{((data?.rows.length ?? 0) === 0) ? 'No special downloads, limits or bonus credits this month.' : 'Nobody matches this filter.'}</div>
      )}
      {(context !== null) && (rows.length > 0) && (
        <ul className="limit-list">
          {rows.map((row) => <LimitItem key={limitTargetKey(row.scope, row.scope_id)} row={row} context={context} />)}
        </ul>
      )}
    </Panel>
  );
}

function FindLimit({ onFind }: { onFind: (target: { scope: LimitScope; id: number }) => void }) {
  const [scope, setScope] = useState<LimitScope>('chat');
  const [text, setText] = useState('');
  const [error, setError] = useState<string | null>(null);
  const inputId = useId();
  const hintId = useId();

  const submit = (event: FormEvent) => {
    event.preventDefault();

    const parsed = parseScopeId(text, scope);
    setError(parsed.error);

    if (parsed.id !== null) {
      onFind({ scope, id: parsed.id });
    }
  };

  return (
    <form className="find-limit" onSubmit={submit} noValidate aria-label="Find a person or group by id">
      <Segmented label="Find a" options={FIND_OPTIONS} value={scope} onChange={setScope} />
      <label className="visually-hidden" htmlFor={inputId}>Id</label>
      <input
        id={inputId}
        className="input input--compact"
        type="text"
        inputMode="numeric"
        placeholder={(scope === 'chat') ? '-1001234567890' : '123456789'}
        value={text}
        aria-describedby={hintId}
        aria-invalid={error !== null}
        onChange={(event) => setText(event.target.value)}
      />
      <button type="submit" className="button button--small button--primary">Find</button>
      <p id={hintId} className="find-limit__hint">{ID_SIGN_HINT}</p>
      {(error !== null) && <p className="field__error" role="alert">{error}</p>}
    </form>
  );
}

interface FoundLimitProps {
  target: { scope: LimitScope; id: number };
  context: LimitsContext;
  refreshKey: string;
}

/** The person or group found by id, read as stored, with the same actions as the list. */
function FoundLimit({ target, context, refreshKey }: FoundLimitProps) {
  const query = useApi((signal) => getLimitScope(target.scope, target.id, signal), [target.scope, target.id, refreshKey]);
  const found = query.data;

  return (
    <div className="found-limit">
      {(query.error !== null) && <p className="field__error" role="alert">Couldn’t find it ({query.error}).</p>}
      {query.loading && (found === null) && <div className="state state--loading" role="status"><span className="spinner" aria-hidden="true" />Loading…</div>}
      {(found !== null) && (
        <ul className="limit-list">
          <LimitItem row={found.row} context={{ ...context, applying: found.applying, rules: found.rules, defaults: found.defaults }} />
        </ul>
      )}
    </div>
  );
}

const SCOPE_LABELS: Record<LimitScope, string> = { user: 'Person', chat: 'Group' };

function lastChangeText(row: LimitRow, context: LimitsContext): string | null {
  const change = row.last_change;
  const defaultLimit = (row.scope === 'chat') ? context.defaults.chat : context.defaults.user;
  const when = (change === null) ? '' : formatTimestamp(change.at, context.timezone);
  const who = (change === null) ? '' : actorLabel(change.actor);

  return (change === null) ? null : limitChangeText(change, defaultLimit, who, when);
}

function LimitItem({ row, context }: { row: LimitRow; context: LimitsContext }) {
  const [confirming, setConfirming] = useState<{ next: number | null } | null>(null);
  const [isEditing, setEditing] = useState(false);
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const followed = useRequestFollow(context.onFinal, context.offset);
  const inputId = useId();
  const request = followed.request;
  const openRequest = row.open_request;
  const now = serverNow(context.offset);
  const isPending = (request !== null) && shouldPollRequest(request.status, request.expires_at, now);
  const reason = canChange(row, context);
  const isLocked = (reason !== null) || sending || isPending;
  const targets = (context.rules === null) ? [] : quickTargets(row, context.rules);
  const base = row.effective_limit ?? 0;
  const parsed = (context.rules === null) ? null : parseLimitInput(text, context.rules);
  const ceiling = ceilingNote(row, context.rules);
  const unlimited = unlimitedSwitch(row, context.rules);
  const change = lastChangeText(row, context);
  const isWaitingReason = (reason !== null) && (openRequest !== null) && (request !== null);

  // A change queued elsewhere (another tab, or before a reload) is followed too, so its outcome shows.
  useEffect(() => {
    const isNew = (openRequest !== null) && ((request === null) || (request.id !== openRequest.id));

    if (isNew && isOpenStatus(openRequest.status)) {
      followed.follow(openRequest);
    }
    // Only a new open request starts following; the request in hand is compared, not watched.
  }, [openRequest?.id]);

  const propose = (next: number | null) => {
    setSendError(null);
    followed.clear();
    setConfirming({ next });
  };

  const send = (next: number | null) => {
    setSending(true);
    setSendError(null);
    queueLimitChange({ scope: row.scope, id: row.scope_id, limit: next, expected: row.stored_limit })
      .then((response) => {
        setConfirming(null);
        setEditing(false);
        setText('');
        followed.follow(response.request);
      })
      .catch((error: unknown) => {
        setConfirming(null);
        const status = statusOf(error);
        const message = messageOf(error);
        const isDashboard = isFromDashboard(error);
        setSendError(limitErrorText(status, message, context.applying, isDashboard));
      })
      .finally(() => setSending(false));
  };

  const submitSet = (event: FormEvent) => {
    event.preventDefault();

    if ((parsed !== null) && (parsed.value !== null)) {
      propose(parsed.value);
    }
  };

  return (
    <li className={`limit-item limit-item--${row.state}`}>
      <div className="limit-item__head">
        <span className="limit-item__name" title={rowName(row)}>{rowName(row)}</span>
        <span className="chip">{SCOPE_LABELS[row.scope]}</span>
        <span className={`chip chip--state chip--${row.state}`}>{stateLabel(row.state)}</span>
      </div>
      <div className="limit-item__body">
        <span className="used">
          <Meter used={row.used} limit={((row.effective_limit ?? 0) > 0) ? row.effective_limit : null} />
          <strong>{limitText(row)}</strong>
        </span>
        <span className="limit-item__facts">
          {(row.remaining !== null) && (row.state !== 'off') && (row.state !== 'blocked') && <span>{formatNumber(row.remaining)} left</span>}
          {(row.credits > 0) && <span>Bonus: {formatNumber(row.credits)}</span>}
          <span>Last month: {formatNumber(row.used_last_month)}</span>
          {(row.scope === 'chat') && (row.boosts > 0) && <span>Boosts: {formatNumber(row.boosts)}</span>}
          <span className="limit-item__id">id {row.scope_id}</span>
        </span>
        {(change !== null) && <span className="limit-item__change">{change}</span>}
      </div>
      <div className="limit-actions" role="group" aria-label={`Change the limit of ${rowName(row)}`}>
        {targets.map((target) => (
          <button key={target} type="button" className="button" disabled={isLocked} onClick={() => propose(target)}>
            +{target - base} → {target}
          </button>
        ))}
        <button type="button" className="button" disabled={isLocked} aria-expanded={isEditing} onClick={() => setEditing((value) => (value === false))}>
          Set…
        </button>
        {(row.limit_source === 'custom') && (
          <button type="button" className="button button--quiet" disabled={isLocked} onClick={() => propose(null)}>Reset to default</button>
        )}
        {(unlimited === 'on') && (
          <button type="button" className="button button--quiet" disabled={isLocked} onClick={() => propose(UNLIMITED)}>Make unlimited</button>
        )}
        {(unlimited === 'off') && (
          <button type="button" className="button" disabled={isLocked} onClick={() => propose(null)}>Turn off unlimited</button>
        )}
      </div>
      {isEditing && (context.rules !== null) && (
        <form className="limit-set" onSubmit={submitSet} noValidate aria-label={`Set the limit of ${rowName(row)}`}>
          <label className="visually-hidden" htmlFor={inputId}>Monthly limit</label>
          <input
            id={inputId}
            className="input limit-set__input"
            type="number"
            inputMode="numeric"
            min={context.rules.min}
            max={context.rules.max}
            step={1}
            value={text}
            aria-invalid={(text !== '') && (parsed?.error !== null)}
            disabled={isLocked}
            onChange={(event) => setText(event.target.value)}
          />
          <button type="submit" className="button button--primary" disabled={isLocked || (parsed?.value ?? null) === null}>Save</button>
          <button type="button" className="button button--quiet" onClick={() => setEditing(false)}>Cancel</button>
          {(text !== '') && (parsed?.error !== null) && <p className="field__error" role="alert">The limit {parsed?.error}.</p>}
        </form>
      )}
      <div className="limit-item__feedback">
        {(reason !== null) && (isWaitingReason === false) && <p className="limit-item__note">{reason}</p>}
        {(row.scope === 'chat') && (row.enabled === false) && <p className="limit-item__note">{GROUP_DISABLED_NOTE}</p>}
        {(ceiling !== null) && <p className="limit-item__note">{ceiling}</p>}
        {(sendError !== null) && <p className="field__error" role="alert">{sendError}</p>}
        {(confirming !== null) && (
          <ConfirmLimit
            row={row}
            next={confirming.next}
            context={context}
            busy={sending}
            onConfirm={() => send(confirming.next)}
            onCancel={() => setConfirming(null)}
          />
        )}
        {(request !== null) && <RequestStatus request={request} offset={context.offset} />}
      </div>
    </li>
  );
}

interface ConfirmLimitProps {
  row: LimitRow;
  next: number | null;
  context: LimitsContext;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

function ConfirmLimit({ row, next, context, busy, onConfirm, onCancel }: ConfirmLimitProps) {
  const cancelButton = useRef<HTMLButtonElement>(null);
  const text = limitConfirm(row, next, context.defaults, context.resets);

  useEffect(() => {
    cancelButton.current?.focus();
  }, []);

  return (
    <div className={text.danger ? 'notice notice--danger' : 'notice notice--warning'} role="alertdialog" aria-label={`Confirm: ${text.question}`}>
      <p><strong>{text.question}</strong></p>
      {(text.warning !== null) && <p>{text.warning}</p>}
      <p className="limit-confirm__note">{text.note}</p>
      <div className="notice__actions">
        <button ref={cancelButton} type="button" className="button button--quiet" onClick={onCancel} disabled={busy}>Cancel</button>
        <button type="button" className={text.danger ? 'button button--danger' : 'button button--primary'} onClick={onConfirm} disabled={busy}>
          {busy ? 'Sending…' : text.action}
        </button>
      </div>
    </div>
  );
}
