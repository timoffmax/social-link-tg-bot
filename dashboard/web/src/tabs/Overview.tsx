import { getOverview, type NewChatsWeek, type OverviewDaily, type OverviewResponse, type PlatformMixRow } from '../api';
import { StatTile, TileGroup } from '../components/Bits';
import { BarList, ColumnChart, SERIES_1, SERIES_2, type Series } from '../components/Charts';
import type { Column } from '../components/DataTable';
import { Panel } from '../components/Panel';
import {
  formatCompact,
  formatDay,
  formatFullDay,
  formatNumber,
  formatShare,
  formatWeek,
  isAllZero,
  platformLabel,
  sumBy,
} from '../format';
import { useApi, type Query } from '../hooks';
import type { TabProps } from './types';

const DAILY_SERIES: Series[] = [
  { key: 'private', label: 'Private chats', color: SERIES_1 },
  { key: 'group', label: 'Groups', color: SERIES_2 },
];

const NEW_CHATS_SERIES: Series[] = [
  { key: 'private', label: 'Private chats', color: SERIES_1 },
  { key: 'groups', label: 'Groups', color: SERIES_2 },
];

const DAILY_COLUMNS: Column<OverviewDaily>[] = [
  { header: 'Day', cell: (row) => formatFullDay(row.day) },
  { header: 'Private', cell: (row) => formatNumber(row.private), numeric: true },
  { header: 'Groups', cell: (row) => formatNumber(row.group), numeric: true },
];

const MIX_COLUMNS: Column<PlatformMixRow>[] = [
  { header: 'Platform', cell: (row) => platformLabel(row.platform) },
  { header: 'Downloads', cell: (row) => formatNumber(row.downloads), numeric: true },
];

const NEW_CHATS_COLUMNS: Column<NewChatsWeek>[] = [
  { header: 'Week', cell: (row) => formatWeek(row.week) },
  { header: 'Private', cell: (row) => formatNumber(row.private), numeric: true },
  { header: 'Groups', cell: (row) => formatNumber(row.groups), numeric: true },
];

export function OverviewTab({ days, tick, onLoaded }: TabProps) {
  const query = useApi((signal) => getOverview(days, signal).then(onLoaded), [days, tick]);

  return (
    <div className="grid">
      <Hero query={query} days={days} />
      <Tiles query={query} />
      <PlatformMix query={query} />
      <NewChats query={query} />
    </div>
  );
}

function Hero({ query, days }: { query: Query<OverviewResponse>; days: number }) {
  const data = query.data;
  const daily = data?.daily ?? [];
  const shownDays = data?.days ?? days;
  const groupShare = formatShare(sumBy(daily, (row) => row.group), data?.totals.downloads_range ?? 0);

  return (
    <Panel
      title={`Downloads, last ${shownDays} days`}
      subtitle={(data === null) ? undefined : (
        <>
          <span className="hero-figure">{formatNumber(data.totals.downloads_range)}</span>
          <span className="hero-note">{groupShare} from groups</span>
          <span className="hero-note">{formatCompact(data.totals.downloads_all)} all time</span>
        </>
      )}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={isAllZero(daily, (row) => row.private + row.group)}
      emptyText={`No downloads in the last ${shownDays} days.`}
      table={{ columns: DAILY_COLUMNS, rows: daily, rowKey: (row) => row.day }}
      wide
    >
      <ColumnChart
        data={daily}
        xKey="day"
        series={DAILY_SERIES}
        stacked
        formatTick={formatDay}
        formatLabel={formatFullDay}
        height={260}
      />
    </Panel>
  );
}

function Tiles({ query }: { query: Query<OverviewResponse> }) {
  const totals = query.data?.totals ?? null;
  const pick = (value: number | undefined) => (value === undefined) ? null : value;

  return (
    <TileGroup label="Totals" loading={query.loading} error={(totals === null) ? query.error : null}>
      <StatTile label="Downloads, 7 days" value={pick(totals?.downloads_7d)} />
      <StatTile label="Downloads, 30 days" value={pick(totals?.downloads_30d)} />
      <StatTile label="Active users, 7 days" value={pick(totals?.active_users_7d)} note={(totals === null) ? undefined : `${formatNumber(totals.active_users_30d)} in 30 days`} />
      <StatTile label="Active chats, 7 days" value={pick(totals?.active_chats_7d)} note={(totals === null) ? undefined : `${formatNumber(totals.active_chats_30d)} in 30 days`} />
      <StatTile label="Groups, all time" value={pick(totals?.groups_all)} />
      <StatTile label="Private users, all time" value={pick(totals?.private_users_all)} />
    </TileGroup>
  );
}

function PlatformMix({ query }: { query: Query<OverviewResponse> }) {
  const rows = query.data?.platform_mix ?? [];

  return (
    <Panel
      title="Platform mix"
      subtitle="Downloads per platform in the selected range"
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={isAllZero(rows, (row) => row.downloads)}
      emptyText="No downloads in this range."
      table={{ columns: MIX_COLUMNS, rows, rowKey: (row) => row.platform }}
    >
      <BarList data={rows} labelKey="platform" valueKey="downloads" label="Downloads" formatCategory={platformLabel} />
    </Panel>
  );
}

function NewChats({ query }: { query: Query<OverviewResponse> }) {
  const rows = query.data?.new_chats_weekly ?? [];

  return (
    <Panel
      title="New chats per week"
      subtitle="Chats whose first download fell in that week"
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={isAllZero(rows, (row) => row.groups + row.private)}
      emptyText="No new chats in this range."
      table={{ columns: NEW_CHATS_COLUMNS, rows, rowKey: (row) => row.week }}
    >
      <ColumnChart
        data={rows}
        xKey="week"
        series={NEW_CHATS_SERIES}
        stacked
        formatTick={formatDay}
        formatLabel={formatWeek}
      />
    </Panel>
  );
}
