import { getPlatforms, type MediaTypeRow, type PlatformHealth, type PlatformsResponse } from '../api';
import { StatusBadge } from '../components/Bits';
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
  platformSeries,
  type PlatformSeries,
} from '../format';
import { useApi, type Query } from '../hooks';
import type { TabProps } from './types';

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

export function PlatformsTab({ days, tick, onLoaded }: TabProps) {
  const query = useApi((signal) => getPlatforms(days, signal).then(onLoaded), [days, tick]);

  return (
    <div className="grid">
      <Health query={query} />
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
