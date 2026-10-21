import { getPlatforms, type MediaTypeRow, type PlatformHealth, type PlatformsResponse, type ServiceRow } from '../api';
import { StaleNotice, StateBadge, StatusBadge } from '../components/Bits';
import { SERIES_1, SERIES_2, SparkArea, StackedBarList, type Series } from '../components/Charts';
import { DataTable, type Column } from '../components/DataTable';
import { Panel } from '../components/Panel';
import {
  formatChangePct,
  formatCompact,
  formatDecimal,
  formatFullDay,
  formatNumber,
  formatRelative,
  formatTimestamp,
  isAllZero,
  platformLabel,
  PLATFORM_ORDER,
  platformSeries,
  type PlatformSeries,
} from '../format';
import { useApi, type Query } from '../hooks';
import {
  errorText,
  groupServices,
  NO_SIGNAL_TEXT,
  probeText,
  serviceBadge,
  serviceKindLabel,
  SERVICES_EMPTY_TEXT,
  servicesSubtitle,
  SHARED_GROUP,
  SHARED_GROUP_LABEL,
  trafficText,
} from '../services';
import type { TabProps } from './types';

/** Used only before the first answer, when no rows are shown. */
const FALLBACK_TIMEZONE = 'UTC';

const MEDIA_SERIES: Series[] = [
  { key: 'video', label: 'Videos', color: SERIES_1 },
  { key: 'photo', label: 'Photos', color: SERIES_2 },
];

const MEDIA_COLUMNS: Column<MediaTypeRow>[] = [
  { header: 'Platform', cell: (row) => platformLabel(row.platform) },
  { header: 'Videos', cell: (row) => formatNumber(row.video), numeric: true },
  { header: 'Photos', cell: (row) => formatNumber(row.photo), numeric: true },
];

const SERIES_COLUMNS: Column<PlatformSeries>[] = [
  { header: 'Platform', cell: (row) => platformLabel(row.platform) },
  { header: 'Downloads', cell: (row) => formatNumber(row.total), numeric: true },
  { header: 'Busiest day', cell: (row) => formatNumber(row.peak), numeric: true },
];

function healthColumns(data: PlatformsResponse): Column<PlatformHealth>[] {
  return [
    { header: 'Platform', cell: (row) => platformLabel(row.platform), fill: true },
    { header: 'Status', cell: (row) => <StatusBadge status={row.status} /> },
    {
      header: 'Last download',
      cell: (row) => <time dateTime={row.last_download_at ?? undefined} title={formatTimestamp(row.last_download_at, data.timezone)}>{formatRelative(row.last_download_at, data.generated_at)}</time>,
      detail: true,
    },
    { header: '24 h', cell: (row) => formatNumber(row.downloads_24h), numeric: true, detail: true },
    { header: 'Per day, 7 d', cell: (row) => formatDecimal(row.avg_daily_7d), numeric: true, detail: true },
    { header: 'Per day, prev 28 d', cell: (row) => formatDecimal(row.avg_daily_prev_28d), numeric: true, detail: true },
    { header: 'Change', cell: (row) => <span className={changeClass(row)}>{formatChangePct(row.change_pct)}</span>, numeric: true },
  ];
}

// Only a change the health check flags is colored; small swings stay neutral.
function changeClass(row: PlatformHealth): string {
  const isFlagged = (row.status !== 'ok');

  return isFlagged ? 'delta delta--down' : 'delta';
}

function ServiceName({ row }: { row: ServiceRow }) {
  return (
    <div>
      <strong>{row.label}</strong> <span className="chip">{serviceKindLabel(row.kind)}</span>
      {(row.host !== null) && <div className="paid-card__details">{row.host}</div>}
    </div>
  );
}

function serviceColumns(now: number, timeZone: string): Column<ServiceRow>[] {
  return [
    { header: 'Service', cell: (row) => <ServiceName row={row} />, fill: true },
    { header: 'Used for', cell: (row) => <span className="paid-card__details">{row.role}</span>, detail: true },
    { header: 'Status', cell: (row) => <ServiceStatus row={row} now={now} /> },
    { header: 'Traffic', cell: (row) => trafficText(row, now, timeZone), detail: true },
    { header: 'Hourly check', cell: (row) => probeText(row, now), detail: true },
    { header: 'Last error', cell: (row) => <ServiceError row={row} />, detail: true },
  ];
}

function ServiceStatus({ row, now }: { row: ServiceRow; now: number }) {
  const badge = serviceBadge(row, now);

  return <StateBadge tone={badge.tone} text={badge.text} />;
}

function ServiceError({ row }: { row: ServiceRow }) {
  const text = errorText(row);

  return (text === null) ? NO_SIGNAL_TEXT : <span className="relay__error">{text}</span>;
}

function groupTitle(key: string): string {
  const result = (key === SHARED_GROUP) ? SHARED_GROUP_LABEL : platformLabel(key);

  return result;
}

export function PlatformsTab({ days, tick, onLoaded }: TabProps) {
  const query = useApi((signal) => getPlatforms(days, signal).then(onLoaded), [days, tick]);

  return (
    <div className="grid">
      <Health query={query} />
      <Services query={query} />
      <Daily query={query} days={days} />
      <MediaTypes query={query} />
    </div>
  );
}

function Health({ query }: { query: Query<PlatformsResponse> }) {
  const data = query.data;
  const rows = data?.health ?? [];
  const flagged = rows.filter((row) => row.status !== 'ok').length;
  const basis = 'Last 7 days against the 28 days before (before the last download for a silent platform).';
  const subtitle = (flagged === 0) ? `All platforms look normal. ${basis}` : `${flagged} of ${rows.length} need a look. ${basis}`;

  return (
    <Panel
      title="Platform health"
      subtitle={(rows.length === 0) ? undefined : subtitle}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={rows.length === 0}
      emptyText="No platform had a download in the last 90 days."
      wide
    >
      {(data !== null) && <DataTable columns={healthColumns(data)} rows={rows} rowKey={(row) => row.platform} caption="Platform health" />}
    </Panel>
  );
}

function Services({ query }: { query: Query<PlatformsResponse> }) {
  const data = query.data;
  const services = data?.services ?? null;
  const rows = services?.data.services ?? [];
  const groups = groupServices(rows, PLATFORM_ORDER);
  const now = (data === null) ? 0 : Date.parse(data.generated_at);
  const columns = serviceColumns(now, data?.timezone ?? FALLBACK_TIMEZONE);

  return (
    <Panel
      title="External services"
      subtitle={(rows.length === 0) ? undefined : servicesSubtitle(rows)}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={rows.length === 0}
      emptyText={SERVICES_EMPTY_TEXT}
      wide
    >
      {(services !== null) && services.is_stale && <StaleNotice updatedAt={services.updated_at} now={now} />}
      {(rows.length > 0) && (
        <div className="relay">
          {groups.map((group) => (
            <section key={group.key} className="relay-history" aria-label={groupTitle(group.key)}>
              <h3 className="relay-history__title">{groupTitle(group.key)}</h3>
              <DataTable columns={columns} rows={group.rows} rowKey={(row) => row.key} caption={`${groupTitle(group.key)} services`} />
            </section>
          ))}
        </div>
      )}
    </Panel>
  );
}

function Daily({ query, days }: { query: Query<PlatformsResponse>; days: number }) {
  const data = query.data;
  const series = (data === null) ? [] : platformSeries(data.platforms, data.daily);
  const shownDays = data?.days ?? days;

  return (
    <Panel
      title="Daily downloads per platform"
      subtitle={`Last ${shownDays} days. Each chart has its own scale, so compare shapes, not heights.`}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={isAllZero(series, (row) => row.total)}
      emptyText="No downloads in this range."
      table={{ columns: SERIES_COLUMNS, rows: series, rowKey: (row) => row.platform }}
      wide
    >
      <div className="multiples">
        {series.map((item) => (
          <div key={item.platform} className="multiple">
            <div className="multiple__head">
              <span className="multiple__name">{platformLabel(item.platform)}</span>
              <span className="multiple__total">{formatCompact(item.total)}</span>
            </div>
            <SparkArea points={item.points} label="Downloads" formatLabel={formatFullDay} />
            <div className="multiple__foot">Peak {formatNumber(item.peak)} a day</div>
          </div>
        ))}
      </div>
    </Panel>
  );
}

function MediaTypes({ query }: { query: Query<PlatformsResponse> }) {
  const rows = query.data?.media_types ?? [];

  return (
    <Panel
      title="Videos and photos"
      subtitle="Media sent per platform in the selected range"
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={isAllZero(rows, (row) => row.photo + row.video)}
      emptyText="No media sent in this range."
      table={{ columns: MEDIA_COLUMNS, rows, rowKey: (row) => row.platform }}
      wide
    >
      <StackedBarList data={rows} labelKey="platform" series={MEDIA_SERIES} formatCategory={platformLabel} />
    </Panel>
  );
}
