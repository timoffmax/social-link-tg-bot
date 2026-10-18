import { useId, useState, type ReactNode } from 'react';
import type { HealthStatus } from '../api';
import { formatCompact, isLongText } from '../format';

interface StatTileProps {
  label: string;
  value: number | null;
  /** Pre-formatted value, e.g. a percentage. */
  display?: string;
  note?: ReactNode;
}

export function StatTile({ label, value, display, note }: StatTileProps) {
  const shown = display ?? ((value === null) ? '–' : formatCompact(value));

  return (
    <div className="tile">
      <div className="tile__label">{label}</div>
      <div className="tile__value">{shown}</div>
      {(note !== undefined) && <div className="tile__note">{note}</div>}
    </div>
  );
}

interface TileGroupProps {
  label: string;
  loading: boolean;
  error: string | null;
  children: ReactNode;
}

/** A row of stat tiles with its own loading and error state. */
export function TileGroup({ label, loading, error, children }: TileGroupProps) {
  return (
    <section className="tiles" aria-label={label} aria-busy={loading}>
      {(error !== null) && <div className="state state--error tiles__error" role="alert"><strong>Couldn’t load the totals</strong> ({error}). They retry on the next refresh.</div>}
      {children}
    </section>
  );
}

const STATUS_TEXT: Record<HealthStatus, { icon: string; label: string; hint: string }> = {
  ok: { icon: '✓', label: 'OK', hint: 'Downloads look normal' },
  drop: { icon: '↓', label: 'Drop', hint: 'Last 7 days are under half of the previous 28-day average' },
  stale: { icon: '✕', label: 'Stale', hint: 'No download for 48 hours although it is normally used daily' },
};

export function StatusBadge({ status }: { status: HealthStatus }) {
  const text = STATUS_TEXT[status];

  return (
    <span className={`badge badge--${status}`} title={text.hint}>
      <span aria-hidden="true" className="badge__icon">{text.icon}</span>
      {text.label}
    </span>
  );
}

/** A blog post's state: published posts are listed under /owner → Broadcast in the bot. */
export function PublishBadge({ published }: { published: boolean }) {
  return (
    <span className={published ? 'badge badge--published' : 'badge badge--draft'}>
      <span aria-hidden="true" className="badge__icon">{published ? '✓' : '–'}</span>
      {published ? 'Published' : 'Draft'}
    </span>
  );
}

export interface LegendItem {
  label: string;
  color: string;
  shape?: 'rect' | 'line';
}

export function Legend({ items }: { items: LegendItem[] }) {
  return (
    <ul className="legend">
      {items.map((item) => (
        <li key={item.label}>
          <span
            aria-hidden="true"
            className={((item.shape ?? 'rect') === 'line') ? 'legend__key legend__key--line' : 'legend__key'}
            style={{ background: item.color }}
          />
          {item.label}
        </li>
      ))}
    </ul>
  );
}

/** User text, rendered as text (React escapes it); long text is clamped. */
export function ClampedText({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const id = useId();
  const isLong = isLongText(text);
  const className = (isLong && (expanded === false)) ? 'message message--clamped' : 'message';

  return (
    <div>
      <p id={id} className={className}>{text}</p>
      {isLong && (
        <button
          type="button"
          className="link-button"
          aria-expanded={expanded}
          aria-controls={id}
          onClick={() => setExpanded((value) => (value === false))}
        >
          {expanded ? 'Show less' : 'Show full text'}
        </button>
      )}
    </div>
  );
}

export interface SegmentOption<T extends string | number> {
  value: T;
  label: string;
}

interface SegmentedProps<T extends string | number> {
  label: string;
  options: SegmentOption<T>[];
  value: T;
  onChange: (value: T) => void;
}

export function Segmented<T extends string | number>({ label, options, value, onChange }: SegmentedProps<T>) {
  return (
    <div className="segmented" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          className="segmented__option"
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

/** Used vs a known limit as a same-ramp meter; without a limit nothing is drawn. */
export function Meter({ used, limit }: { used: number; limit: number | null }) {
  const hasLimit = (limit !== null) && (limit > 0);
  const share = hasLimit ? Math.min(1, used / limit) : 0;
  const level = (share >= 1) ? 'full' : ((share >= 0.8) ? 'high' : 'normal');

  return hasLimit ? (
    <span className={`meter meter--${level}`} aria-hidden="true">
      <span className="meter__fill" style={{ width: `${share * 100}%` }} />
    </span>
  ) : null;
}
