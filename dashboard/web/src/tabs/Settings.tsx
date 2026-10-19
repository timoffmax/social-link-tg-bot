import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent, type ReactElement } from 'react';
import {
  ApiError,
  getSettingRequest,
  getSettings,
  getSettingsAudit,
  HTTP_BAD_REQUEST,
  HTTP_CONFLICT,
  queueSettingChange,
  type AuditEntry,
  type AuditFilter,
  type AuditResponse,
  type SettingRequest,
  type SettingRow,
  type SettingSource,
  type SettingsResponse,
  type SettingValue,
} from '../api';
import type { Column } from '../components/DataTable';
import { DataTable } from '../components/DataTable';
import { Panel } from '../components/Panel';
import { formatRelative, formatTimestamp } from '../format';
import { useApi } from '../hooks';
import {
  actorLabel,
  APPLIED_SHOWN_MS,
  clockOffset,
  conflictText,
  confirmText,
  formatValue,
  groupLabel,
  isOpenStatus,
  isQueueStuck,
  minutesLeft,
  missingTokenText,
  moveItem,
  needsApproval,
  numberStep,
  parseNumber,
  pollDelay,
  rangeText,
  sameValue,
  serverNow,
  SETTINGS_REFRESH_MS,
  shouldPollRequest,
  SOURCE_LABELS,
  upperBound,
  validateValue,
} from '../settings';
import type { TabProps } from './types';

const APPROVAL_NOTE = 'Needs the owner’s approval in Telegram (coming soon): this change can’t be made from here yet.';
const ALL_FILTER = '';
const GROUP_FILTER_PREFIX = 'group:';
const KEY_FILTER_PREFIX = 'key:';

function messageOf(error: unknown): string {
  const result = (error instanceof Error) ? error.message : 'Unknown error';

  return result;
}

function statusOf(error: unknown): number {
  const result = (error instanceof ApiError) ? error.status : 0;

  return result;
}

/** A refused change as one sentence for its row; isStuck: only a running bot can clear the waiting change. */
function sendErrorText(error: unknown, isStuck: boolean): string {
  const status = statusOf(error);
  let result = `The server didn’t accept the change (${messageOf(error)}). Try again.`;

  if (status === HTTP_CONFLICT) {
    result = conflictText(isStuck);
  }

  if (status === HTTP_BAD_REQUEST) {
    result = `Not sent: ${messageOf(error)}.`;
  }

  return result;
}

/** A counter that bumps every 30 s while the page is visible and on demand. */
function useSettingsTick(): [number, () => void] {
  const [tick, setTick] = useState(0);
  const bump = useCallback(() => setTick((value) => value + 1), []);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        bump();
      }
    }, SETTINGS_REFRESH_MS);

    return () => window.clearInterval(timer);
  }, [bump]);

  return [tick, bump];
}

export function SettingsTab({ tick, onLoaded }: TabProps) {
  const [localTick, reload] = useSettingsTick();
  const query = useApi((signal) => getSettings(signal).then(onLoaded), [tick, localTick]);
  const [auditFilter, setAuditFilter] = useState(ALL_FILTER);
  const auditPanel = useRef<HTMLDivElement>(null);
  const data = query.data;
  const groups = useMemo(() => settingGroups(data), [data]);
  // Measured on the render after each load, so expiry decisions follow the server's clock.
  const offset = useMemo(() => ((data === null) ? 0 : clockOffset(data.generated_at)), [data]);

  const showHistory = useCallback((key: string) => {
    setAuditFilter(`${KEY_FILTER_PREFIX}${key}`);
    auditPanel.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, []);

  return (
    <div className="grid">
      {(data !== null) && <Banners data={data} />}
      {(data !== null) && (data.settings.length === 0) && (
        <div className="notice notice--warning panel--wide" role="status">
          <p>The bot has not published its settings yet. They appear here a few seconds after it starts.</p>
        </div>
      )}
      {((data === null) || (data.settings.length > 0)) && groups.map((group, index) => (
        <Panel
          key={group.id}
          title={groupLabel(group.id)}
          loading={query.loading}
          error={(index === 0) ? query.error : null}
          hasData={data !== null}
          isEmpty={group.settings.length === 0}
          emptyText="No settings in this group."
          wide
        >
          {(data !== null) && (
            <ul className="settings">
              {group.settings.map((setting) => (
                <SettingItem key={setting.key} setting={setting} data={data} offset={offset} onFinal={reload} onShowHistory={showHistory} />
              ))}
            </ul>
          )}
        </Panel>
      ))}
      {(data === null) && (groups.length === 0) && (
        <Panel title="Settings" loading={query.loading} error={query.error} hasData={false} isEmpty emptyText="" wide>
          {null}
        </Panel>
      )}
      <div ref={auditPanel} className="panel--wide settings__audit-anchor">
        <AuditLog data={data} filter={auditFilter} onFilterChange={setAuditFilter} refreshKey={`${tick}-${localTick}`} />
      </div>
    </div>
  );
}

interface SettingGroup {
  id: string;
  settings: SettingRow[];
}

/** The settings by group, in the published group order; a group the order misses comes last. */
function settingGroups(data: SettingsResponse | null): SettingGroup[] {
  const settings = data?.settings ?? [];
  const order = [...(data?.groups ?? [])];

  for (const setting of settings) {
    if (order.includes(setting.group) === false) {
      order.push(setting.group);
    }
  }

  return order.map((id) => ({ id, settings: settings.filter((setting) => (setting.group === id)) }));
}

function Banners({ data }: { data: SettingsResponse }) {
  const bot = data.bot;
  const profileError = data.profile?.error ?? '';

  return (
    <>
      {(bot === null) && (
        <div className="notice notice--warning panel--wide" role="status">
          <p>The bot has not reported yet: changes wait for it and expire after 10 minutes.</p>
        </div>
      )}
      {(bot !== null) && bot.is_stale && (
        <div className="notice notice--danger panel--wide" role="alert">
          <p>
            The bot is not running (last heartbeat {formatRelative(bot.heartbeat_at, data.generated_at)}): changes will wait and
            expire after 10 minutes.
          </p>
        </div>
      )}
      {(data.applying === false) && (
        <div className="notice notice--warning panel--wide" role="status">
          <p>The bot does not apply changes yet, so changing settings here is off. The values below are what it uses now.</p>
        </div>
      )}
      {(bot !== null) && (bot.settings_source === 'cache') && (
        <div className="notice notice--warning panel--wide" role="status">
          <p>The database was down when the bot started, so it runs on its cached settings. Restart the bot once the database is back.</p>
        </div>
      )}
      {(profileError !== '') && (
        <div className="notice notice--danger panel--wide" role="alert">
          <p>The bot could not update its Telegram profile after the last change ({profileError}). It tries again after the next change.</p>
        </div>
      )}
    </>
  );
}

/** The value the bot uses now; null until it has published its values. */
function currentOf(setting: SettingRow): SettingValue | null {
  return setting.effective?.value ?? null;
}

interface Draft {
  /** Number, host and select fields. */
  text: string;
  /** The provider order. */
  list: string[];
}

function draftOf(value: SettingValue): Draft {
  const list = Array.isArray(value) ? value : [];
  const text = Array.isArray(value) ? '' : String(value);

  return { text, list };
}

interface Proposal {
  value: SettingValue | null;
  error: string | null;
}

/** The value a draft stands for and why the bot would refuse it. */
function proposalOf(setting: SettingRow, draft: Draft): Proposal {
  const readers: Record<SettingRow['type'], () => SettingValue | null> = {
    bool: () => (draft.text === 'true'),
    int: () => parseNumber(draft.text),
    decimal: () => parseNumber(draft.text),
    enum: () => draft.text,
    host: () => draft.text.toLowerCase(),
    ordered_list: () => draft.list,
  };
  const value = readers[setting.type]();
  const error = (value === null) ? 'must be a number' : validateValue(setting, value);

  return { value, error };
}

/**
 * Follows one queued change until the bot decides it or it expires: every second for 15 s, then
 * every 5 s. A loop goes on only while the row is mounted and no newer loop has replaced it.
 */
function useRequestFollow(onFinal: () => void, offset: number) {
  const [request, setRequest] = useState<SettingRequest | null>(null);
  const loop = useRef<{ timer: number | undefined; controller: AbortController } | null>(null);
  const finalRef = useRef(onFinal);
  const offsetRef = useRef(offset);
  finalRef.current = onFinal;
  offsetRef.current = offset;

  const stop = useCallback(() => {
    const current = loop.current;

    if (current !== null) {
      window.clearTimeout(current.timer);
      current.controller.abort();
    }

    loop.current = null;
  }, []);

  const follow = useCallback((started: SettingRequest) => {
    stop();
    const startedAt = Date.now();
    const own = { timer: undefined as number | undefined, controller: new AbortController() };
    loop.current = own;
    setRequest(started);
    const isCurrent = () => (loop.current === own);
    const schedule = (latest: SettingRequest) => {
      const now = serverNow(offsetRef.current);
      const isWaiting = shouldPollRequest(latest.status, latest.expires_at, now);

      if (isCurrent() && isWaiting) {
        own.timer = window.setTimeout(poll, pollDelay(Date.now() - startedAt));
      }

      if (isCurrent() && (isWaiting === false)) {
        loop.current = null;
        finalRef.current();
      }
    };
    const poll = () => {
      getSettingRequest(started.id, own.controller.signal)
        .then((response) => {
          if (isCurrent()) {
            setRequest(response.request);
            schedule(response.request);
          }
        })
        .catch(() => {
          // A failed check (network, restart) is retried until the change expires.
          if (isCurrent()) {
            schedule(started);
          }
        });
    };
    schedule(started);
  }, [stop]);

  const clear = useCallback(() => {
    stop();
    setRequest(null);
  }, [stop]);

  useEffect(() => stop, [stop]);

  return { request, follow, clear };
}

interface Confirming {
  next: SettingValue | null;
}

interface SettingItemProps {
  setting: SettingRow;
  data: SettingsResponse;
  /** Server clock minus browser clock, from the last settings load. */
  offset: number;
  /** A followed change is decided or expired: reload the settings. */
  onFinal: () => void;
  onShowHistory: (key: string) => void;
}

function SettingItem({ setting, data, offset, onFinal, onShowHistory }: SettingItemProps) {
  const current = currentOf(setting);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [confirming, setConfirming] = useState<Confirming | null>(null);
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const followed = useRequestFollow(onFinal, offset);
  const controlId = useId();
  const noteId = useId();
  const openRequest = setting.open_request;
  const request = followed.request;
  const now = serverNow(offset);
  const isPending = (request !== null) && shouldPollRequest(request.status, request.expires_at, now);
  const canChange = (current !== null) && data.applying && (sending === false) && (isPending === false);
  const proposal = ((draft === null) || (current === null)) ? null : proposalOf(setting, draft);
  const isChanged = (proposal !== null) && (proposal.value !== null) && (sameValue(proposal.value, current) === false);
  const isApprovalNeeded = isChanged && (proposal?.value !== null) && needsApproval(setting.approval, current as SettingValue, proposal?.value as SettingValue);
  const canSave = canChange && isChanged && (proposal?.error === null) && (isApprovalNeeded === false);
  const tokenNote = missingTokenText(setting, data.secrets);

  // A change queued elsewhere (another tab, or before a reload) is followed too, so its outcome shows.
  useEffect(() => {
    const isNew = (openRequest !== null) && ((request === null) || (request.id !== openRequest.id));

    if (isNew && isOpenStatus(openRequest.status)) {
      followed.follow({
        ...openRequest,
        key: setting.key,
        expected: openRequest.value ?? setting.default,
        needs_approval: null,
        error: null,
        decided_at: null,
      });
    }
    // Only a new open request starts following; the request in hand is compared, not watched.
  }, [openRequest?.id]);

  const send = (next: SettingValue | null) => {
    if (current !== null) {
      setSending(true);
      setSendError(null);
      queueSettingChange({ key: setting.key, value: next, expected: current })
        .then((response) => {
          setConfirming(null);
          setDraft(null);
          followed.follow(response.request);
        })
        .catch((error: unknown) => {
          setConfirming(null);

          const sentAt = serverNow(offset);
          const isStuck = isQueueStuck(data.bot, setting.open_request, sentAt);

          const text = sendErrorText(error, isStuck);

          setSendError(text);
        })
        .finally(() => setSending(false));
    }
  };

  const propose = (next: SettingValue | null) => {
    const isBlocked = (next !== null) && (current !== null) && needsApproval(setting.approval, current, next);

    setSendError(isBlocked ? APPROVAL_NOTE : null);
    followed.clear();

    if (isBlocked === false) {
      setConfirming({ next });
    }
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();

    if (canSave && (proposal !== null)) {
      propose(proposal.value);
    }
  };

  const edit = (next: Draft) => {
    setDraft(next);
    setSendError(null);
  };

  const cancel = () => {
    setDraft(null);
    setSendError(null);
  };

  const shown = (draft === null) ? draftOf(current ?? setting.default) : draft;

  return (
    <li className="setting">
      <div className="setting__main">
        <div className="setting__head">
          <label className="setting__label" htmlFor={controlId}>{setting.label}</label>
          {(setting.effective !== null) && <SourceBadge source={setting.effective.source} />}
        </div>
        <p id={noteId} className="setting__impact">{setting.impact}</p>
        <SettingMeta setting={setting} data={data} onShowHistory={onShowHistory} />
        {(tokenNote !== null) && <p className="setting__warning">{tokenNote}</p>}
      </div>

      <form className="setting__control" onSubmit={onSubmit} noValidate aria-label={`Change ${setting.label}`}>
        <Control
          setting={setting}
          id={controlId}
          describedBy={noteId}
          current={current}
          shown={shown}
          disabled={(canChange === false)}
          invalid={(proposal?.error ?? null) !== null}
          onEdit={edit}
          onToggle={(next) => propose(next)}
        />
        {(setting.type !== 'bool') && (draft !== null) && (
          <div className="setting__buttons">
            <button type="submit" className="button button--primary button--small" disabled={(canSave === false)}>Save</button>
            <button type="button" className="button button--quiet button--small" onClick={cancel} disabled={sending}>
              Cancel
            </button>
          </div>
        )}
        {(setting.stored !== null) && (draft === null) && (
          <button type="button" className="link-button setting__reset" onClick={() => propose(null)} disabled={(canChange === false)}>
            Reset to {(setting.effective?.env_set === true) ? '.env' : 'default'}
          </button>
        )}
      </form>

      <div className="setting__feedback">
        {(proposal !== null) && isChanged && (proposal.error !== null) && <p className="field__error" role="alert">{capitalize(proposal.error)}.</p>}
        {isApprovalNeeded && (proposal?.error === null) && <p className="setting__approval">{APPROVAL_NOTE}</p>}
        {(sendError !== null) && <p className={(sendError === APPROVAL_NOTE) ? 'setting__approval' : 'field__error'} role="alert">{sendError}</p>}
        {(confirming !== null) && (current !== null) && (
          <ConfirmChange
            setting={setting}
            current={current}
            next={confirming.next}
            busy={sending}
            onConfirm={() => send(confirming.next)}
            onCancel={() => setConfirming(null)}
          />
        )}
        {(request !== null) && <RequestStatus request={request} offset={offset} />}
      </div>
    </li>
  );
}

function capitalize(text: string): string {
  return `${text.charAt(0).toUpperCase()}${text.slice(1)}`;
}

function SourceBadge({ source }: { source: SettingSource }) {
  const text = SOURCE_LABELS[source];

  return <span className={`badge badge--source-${source}`} title={text.hint}>{text.label}</span>;
}

interface SettingMetaProps {
  setting: SettingRow;
  data: SettingsResponse;
  onShowHistory: (key: string) => void;
}

/** Default, .env presence and the last change, under the setting's description. */
function SettingMeta({ setting, data, onShowHistory }: SettingMetaProps) {
  const stored = setting.stored;
  const envVar = setting.env_var ?? '';
  const isEnvSet = (setting.effective?.env_set === true);
  const range = ((setting.type === 'int') || (setting.type === 'decimal')) ? rangeText(setting) : '';

  return (
    <p className="setting__meta">
      <span>Default {formatValue(setting.default)}</span>
      {(envVar !== '') && <span><code>{envVar}</code> {isEnvSet ? 'set in .env' : 'not in .env'}</span>}
      {(range !== '') && <span>{range}</span>}
      {(stored !== null) && (
        <span>
          Changed by {actorLabel(stored.updated_by)},{' '}
          <time dateTime={stored.updated_at} title={formatTimestamp(stored.updated_at, data.timezone)}>
            {formatRelative(stored.updated_at, data.generated_at)}
          </time>
        </span>
      )}
      {(setting.last_change !== null) && (
        <button type="button" className="link-button" onClick={() => onShowHistory(setting.key)}>History</button>
      )}
    </p>
  );
}

interface ControlProps {
  setting: SettingRow;
  id: string;
  describedBy: string;
  current: SettingValue | null;
  shown: Draft;
  disabled: boolean;
  invalid: boolean;
  onEdit: (draft: Draft) => void;
  onToggle: (next: boolean) => void;
}

function Control(props: ControlProps) {
  const { setting, id, describedBy, current, shown, disabled, invalid, onEdit, onToggle } = props;
  const upper = upperBound(setting);
  const controls: Record<SettingRow['type'], () => ReactElement> = {
    bool: () => (
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={current === true}
        aria-describedby={describedBy}
        className="switch"
        disabled={disabled}
        onClick={() => onToggle(current !== true)}
      >
        <span className="switch__track" aria-hidden="true"><span className="switch__thumb" /></span>
        <span className="switch__text">{(current === true) ? 'On' : 'Off'}</span>
      </button>
    ),
    int: () => numberInput(),
    decimal: () => numberInput(),
    enum: () => (
      <select
        id={id}
        className="input setting__select"
        value={shown.text}
        aria-describedby={describedBy}
        disabled={disabled}
        onChange={(event) => onEdit({ ...shown, text: event.target.value })}
      >
        {(setting.enum ?? []).map((name) => <option key={name} value={name}>{name}</option>)}
      </select>
    ),
    host: () => (
      <input
        id={id}
        className="input setting__input"
        type="text"
        value={shown.text}
        aria-describedby={describedBy}
        aria-invalid={invalid}
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        disabled={disabled}
        onChange={(event) => onEdit({ ...shown, text: event.target.value })}
      />
    ),
    ordered_list: () => <OrderedList id={id} setting={setting} list={shown.list} disabled={disabled} onChange={(list) => onEdit({ ...shown, list })} />,
  };

  function numberInput() {
    return (
      <input
        id={id}
        className="input setting__input setting__input--number"
        type="number"
        inputMode={(setting.type === 'decimal') ? 'decimal' : 'numeric'}
        step={numberStep(setting)}
        min={setting.min}
        max={upper}
        value={shown.text}
        aria-describedby={describedBy}
        aria-invalid={invalid}
        disabled={disabled}
        onChange={(event) => onEdit({ ...shown, text: event.target.value })}
      />
    );
  }

  return controls[setting.type]();
}

interface OrderedListProps {
  id: string;
  setting: SettingRow;
  list: string[];
  disabled: boolean;
  onChange: (list: string[]) => void;
}

/** The providers in use, in order, with the unused ones after them; each can be moved or left out. */
function OrderedList({ id, setting, list, disabled, onChange }: OrderedListProps) {
  const unused = (setting.enum ?? []).filter((name) => (list.includes(name) === false));

  const toggle = (name: string, isUsed: boolean) => {
    const next = isUsed ? [...list, name] : list.filter((item) => (item !== name));
    onChange(next);
  };

  return (
    <ol id={id} className="order-list" aria-label={setting.label}>
      {list.map((name, index) => (
        <li key={name} className="order-list__item">
          <label className="checkbox">
            <input type="checkbox" checked onChange={() => toggle(name, false)} disabled={disabled} />
            <span>{index + 1}. {name}</span>
          </label>
          <span className="order-list__moves">
            <button
              type="button"
              className="button button--quiet button--small"
              onClick={() => onChange(moveItem(list, index, -1))}
              disabled={disabled || (index === 0)}
              aria-label={`Move ${name} up`}
            >
              ↑
            </button>
            <button
              type="button"
              className="button button--quiet button--small"
              onClick={() => onChange(moveItem(list, index, 1))}
              disabled={disabled || (index === (list.length - 1))}
              aria-label={`Move ${name} down`}
            >
              ↓
            </button>
          </span>
        </li>
      ))}
      {unused.map((name) => (
        <li key={name} className="order-list__item order-list__item--unused">
          <label className="checkbox">
            <input type="checkbox" checked={false} onChange={() => toggle(name, true)} disabled={disabled} />
            <span>{name} (not used)</span>
          </label>
        </li>
      ))}
    </ol>
  );
}

interface ConfirmChangeProps {
  setting: SettingRow;
  current: SettingValue;
  next: SettingValue | null;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

function ConfirmChange({ setting, current, next, busy, onConfirm, onCancel }: ConfirmChangeProps) {
  const cancelButton = useRef<HTMLButtonElement>(null);
  const text = confirmText(setting, current, next);
  const isStopping = (text.effect !== '');

  useEffect(() => {
    cancelButton.current?.focus();
  }, []);

  return (
    <div className={isStopping ? 'notice notice--danger' : 'notice notice--warning'} role="alertdialog" aria-label={`Confirm: ${text.question}`}>
      <p><strong>{text.question}</strong>{isStopping && <> {text.effect}</>}</p>
      <div className="notice__actions">
        <button type="button" className={isStopping ? 'button button--danger' : 'button button--primary'} onClick={onConfirm} disabled={busy}>
          {busy ? 'Sending…' : text.action}
        </button>
        <button ref={cancelButton} type="button" className="button button--quiet" onClick={onCancel} disabled={busy}>Cancel</button>
      </div>
    </div>
  );
}

/** Where a queued change stands; an applied one fades after a few seconds. */
function RequestStatus({ request, offset }: { request: SettingRequest; offset: number }) {
  const [isFaded, setFaded] = useState(false);
  const now = serverNow(offset);
  const isOpen = isOpenStatus(request.status);
  const isOverdue = isOpen && (shouldPollRequest(request.status, request.expires_at, now) === false);
  const left = minutesLeft(request.expires_at, now);

  useEffect(() => {
    const timer = (request.status === 'applied') ? window.setTimeout(() => setFaded(true), APPLIED_SHOWN_MS) : undefined;

    return () => window.clearTimeout(timer);
  }, [request.status]);

  return (
    <div className={`setting__status setting__status--${request.status}${isFaded ? ' is-faded' : ''}`} role="status">
      {(request.status === 'queued') && (isOverdue === false) && (
        <><span className="spinner" aria-hidden="true" />Waiting for the bot to apply {formatValue(request.value)} (expires in {left} min)</>
      )}
      {(request.status === 'awaiting_approval') && (isOverdue === false) && (
        <>Waiting for your Approve in Telegram (expires in {left} min)</>
      )}
      {isOverdue && <>Expired: the bot did not pick it up in time. Is the bot running?</>}
      {(request.status === 'applied') && <><span aria-hidden="true">✓</span> Applied {(request.value === null) ? 'the reset' : formatValue(request.value)}</>}
      {(request.status === 'rejected') && <>Not applied: {request.error ?? 'refused by the bot'}</>}
      {(request.status === 'expired') && <>Expired: {request.error ?? 'the bot did not apply it within 10 minutes'}</>}
    </div>
  );
}

interface AuditLogProps {
  data: SettingsResponse | null;
  filter: string;
  onFilterChange: (filter: string) => void;
  /** Changes when the settings reload, so new changes show. */
  refreshKey: string;
}

function auditFilterOf(filter: string): AuditFilter {
  const result: AuditFilter = {};

  if (filter.startsWith(GROUP_FILTER_PREFIX)) {
    result.group = filter.slice(GROUP_FILTER_PREFIX.length);
  }

  if (filter.startsWith(KEY_FILTER_PREFIX)) {
    result.key = filter.slice(KEY_FILTER_PREFIX.length);
  }

  return result;
}

function AuditLog({ data, filter, onFilterChange, refreshKey }: AuditLogProps) {
  const query = useApi((signal) => getSettingsAudit(auditFilterOf(filter), signal), [filter, refreshKey]);
  const [older, setOlder] = useState<{ base: AuditResponse; entries: AuditEntry[]; next: number | null } | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState<string | null>(null);
  const filterId = useId();
  const settings = data?.settings ?? [];
  const labels = useMemo(() => new Map(settings.map((setting) => [setting.key, setting.label])), [settings]);
  const first = query.data;
  // Older pages belong to the first page they were loaded after; a reload starts over.
  const extra = ((older !== null) && (older.base === first)) ? older : null;
  const entries = [...(first?.entries ?? []), ...(extra?.entries ?? [])];
  const next = (extra === null) ? (first?.next_before ?? null) : extra.next;
  const timezone = first?.timezone ?? data?.timezone ?? 'UTC';
  const groups = data?.groups ?? [];

  const loadOlder = () => {
    if ((first !== null) && (next !== null)) {
      setLoadingOlder(true);
      setOlderError(null);
      getSettingsAudit({ ...auditFilterOf(filter), before: next })
        .then((response) => setOlder({ base: first, entries: [...(extra?.entries ?? []), ...response.entries], next: response.next_before }))
        .catch((error: unknown) => setOlderError(messageOf(error)))
        .finally(() => setLoadingOlder(false));
    }
  };

  const columns: Column<AuditEntry>[] = [
    { header: 'Time', cell: (row) => formatTimestamp(row.created_at, timezone) },
    { header: 'Setting', cell: (row) => <span title={row.key}>{labels.get(row.key) ?? row.key}</span>, fill: true },
    { header: 'Change', cell: (row) => `${formatValue(row.old_value)} → ${formatValue(row.new_value)}` },
    { header: 'By', cell: (row) => actorLabel(row.actor) },
    { header: 'DB role', cell: (row) => <code>{row.db_role}</code>, detail: true },
    { header: 'Note', cell: (row) => row.note ?? '—', detail: true },
    { header: 'Request', cell: (row) => ((row.request_id === null) ? '—' : `#${row.request_id}`), detail: true },
  ];

  const filterControl = (
    <div className="audit-filter">
      <label className="visually-hidden" htmlFor={filterId}>Show changes of</label>
      <select id={filterId} className="input input--compact" value={filter} onChange={(event) => onFilterChange(event.target.value)}>
        <option value={ALL_FILTER}>All settings</option>
        <optgroup label="Groups">
          {groups.map((group) => <option key={group} value={`${GROUP_FILTER_PREFIX}${group}`}>{groupLabel(group)}</option>)}
        </optgroup>
        <optgroup label="Settings">
          {settings.map((setting) => <option key={setting.key} value={`${KEY_FILTER_PREFIX}${setting.key}`}>{setting.label}</option>)}
        </optgroup>
      </select>
    </div>
  );

  return (
    <Panel
      title="Audit log"
      subtitle="Every change of a setting, newest first, whoever made it (also manual database edits)"
      loading={query.loading}
      error={query.error}
      hasData={first !== null}
      isEmpty={entries.length === 0}
      emptyText={(filter === ALL_FILTER) ? 'No setting has been changed yet.' : 'No changes for this filter.'}
      action={filterControl}
      wide
    >
      <DataTable columns={columns} rows={entries} rowKey={(row) => row.id} caption="Settings changes" />
      {(olderError !== null) && <div className="state state--error" role="alert">Couldn’t load older changes ({olderError}).</div>}
      {(next !== null) && (
        <button type="button" className="button button--small settings__older" onClick={loadOlder} disabled={loadingOlder}>
          {loadingOlder ? 'Loading…' : 'Show older changes'}
        </button>
      )}
    </Panel>
  );
}
