import { useId, useState, type ReactNode } from 'react';
import { DataTable, type Column } from './DataTable';

export interface PanelTable<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string | number;
}

interface PanelProps<T> {
  title: string;
  subtitle?: ReactNode;
  loading: boolean;
  error: string | null;
  hasData: boolean;
  isEmpty: boolean;
  emptyText: string;
  /** A chart panel's table view: the accessible twin of the chart. */
  table?: PanelTable<T>;
  /** A control in the panel header, e.g. "New post". */
  action?: ReactNode;
  wide?: boolean;
  children: ReactNode;
}

export function Panel<T>(props: PanelProps<T>) {
  const { title, subtitle, loading, error, hasData, isEmpty, emptyText, table, action, wide, children } = props;
  const [showTable, setShowTable] = useState(false);
  const headingId = useId();
  const canToggle = (table !== undefined) && hasData && (isEmpty === false);
  const className = ['panel', (wide === true) ? 'panel--wide' : '', (loading && hasData) ? 'is-refetching' : '']
    .filter((name) => name !== '')
    .join(' ');

  return (
    <section className={className} aria-labelledby={headingId} aria-busy={loading}>
      <header className="panel__head">
        <div>
          <h2 id={headingId} className="panel__title">{title}</h2>
          {(subtitle !== undefined) && <p className="panel__subtitle">{subtitle}</p>}
        </div>
        {canToggle && (
          <button
            type="button"
            className="link-button"
            aria-pressed={showTable}
            onClick={() => setShowTable((value) => (value === false))}
          >
            {showTable ? 'Show chart' : 'Show table'}
          </button>
        )}
        {action}
      </header>
      <PanelBody
        loading={loading}
        error={error}
        hasData={hasData}
        isEmpty={isEmpty}
        emptyText={emptyText}
      >
        {(showTable && (table !== undefined)) ? <DataTable {...table} /> : children}
      </PanelBody>
    </section>
  );
}

interface PanelBodyProps {
  loading: boolean;
  error: string | null;
  hasData: boolean;
  isEmpty: boolean;
  emptyText: string;
  children: ReactNode;
}

function PanelBody({ loading, error, hasData, isEmpty, emptyText, children }: PanelBodyProps) {
  const showLoading = loading && (hasData === false);
  const showError = (error !== null);
  const showEmpty = hasData && isEmpty && (showError === false);
  const showContent = hasData && (isEmpty === false);

  return (
    <>
      {showLoading && <div className="state state--loading" role="status"><span className="spinner" aria-hidden="true" />Loading…</div>}
      {showError && (
        <div className="state state--error" role="alert">
          <strong>Couldn’t load this panel</strong> ({error}). {hasData ? 'Showing the last loaded data.' : 'It retries on the next refresh.'}
        </div>
      )}
      {showEmpty && <div className="state state--empty">{emptyText}</div>}
      {showContent && <div className="panel__body">{children}</div>}
    </>
  );
}
