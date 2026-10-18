import { getMoney, MONEY_MONTHS, type GatedCurrent, type GatedMonth, type MoneyResponse, type StarsMonth } from '../api';
import { Meter, StatTile, TileGroup } from '../components/Bits';
import { ColumnChart, SERIES_1, SERIES_2, type Series } from '../components/Charts';
import { DataTable, type Column } from '../components/DataTable';
import { Panel } from '../components/Panel';
import { chatLabel, formatCount, formatFullMonth, formatMonth, formatNumber, isAllZero, userLabel } from '../format';
import { useApi, type Query } from '../hooks';
import type { TabProps } from './types';

const GATED_SERIES: Series[] = [{ key: 'global_used', label: 'Restricted posts', color: SERIES_1 }];

const STARS_SERIES: Series[] = [
  { key: 'stars', label: 'Stars received', color: SERIES_1 },
  { key: 'refunded_stars', label: 'Stars refunded', color: SERIES_2 },
];

const GATED_COLUMNS: Column<GatedMonth>[] = [
  { header: 'Month', cell: (row) => formatFullMonth(row.month) },
  { header: 'All', cell: (row) => formatNumber(row.global_used), numeric: true },
  { header: 'Group quotas', cell: (row) => formatNumber(row.chats_used), numeric: true },
  { header: 'User quotas', cell: (row) => formatNumber(row.users_used), numeric: true },
];

const STARS_COLUMNS: Column<StarsMonth>[] = [
  { header: 'Month', cell: (row) => formatFullMonth(row.month) },
  { header: 'Payments', cell: (row) => formatNumber(row.payments), numeric: true },
  { header: 'Stars', cell: (row) => formatNumber(row.stars), numeric: true },
  { header: 'Boosts', cell: (row) => formatNumber(row.boosts), numeric: true },
  { header: 'Refunds', cell: (row) => formatNumber(row.refunds), numeric: true },
  { header: 'Refunded Stars', cell: (row) => formatNumber(row.refunded_stars), numeric: true },
];

function currentTitle(row: GatedCurrent): string {
  const result = (row.scope === 'user') ? userLabel(row.title, row.scope_id) : chatLabel(row.title, row.scope_id);

  return result;
}

const CURRENT_COLUMNS: Column<GatedCurrent>[] = [
  { header: 'Chat or user', cell: (row) => <span className="cell-title">{currentTitle(row)}</span> },
  { header: 'Kind', cell: (row) => <span className="kind">{(row.scope === 'chat') ? 'Group' : 'User'}</span> },
  {
    header: 'Used',
    cell: (row) => (
      <span className="used">
        <Meter used={row.used} limit={row.limit} />
        {formatNumber(row.used)}{(row.limit === null) ? '' : ` of ${formatNumber(row.limit)}`}
      </span>
    ),
    numeric: true,
  },
  { header: 'Limit', cell: (row) => (row.custom ? 'Custom' : 'Default') },
];

export function MoneyTab({ tick, onLoaded }: TabProps) {
  const query = useApi((signal) => getMoney(MONEY_MONTHS, signal).then(onLoaded), [tick]);

  return (
    <div className="grid">
      <CreditTiles query={query} />
      <GatedMonthly query={query} />
      <StarsMonthly query={query} />
      <GatedThisMonth query={query} />
    </div>
  );
}

function CreditTiles({ query }: { query: Query<MoneyResponse> }) {
  const credits = query.data?.credits ?? null;

  return (
    <TileGroup label="Unused bonus credits" loading={query.loading} error={(credits === null) ? query.error : null}>
      <StatTile label="Bonus credits held by users" value={credits?.user_credits ?? null} note={(credits === null) ? undefined : formatCount(credits.users_with_credits, 'user', 'users')} />
      <StatTile label="Bonus credits held by groups" value={credits?.chat_credits ?? null} note={(credits === null) ? undefined : formatCount(credits.chats_with_credits, 'group', 'groups')} />
    </TileGroup>
  );
}

function GatedMonthly({ query }: { query: Query<MoneyResponse> }) {
  const rows = query.data?.gated_monthly ?? [];
  const extra = (row: GatedMonth) => [
    { label: 'Group quotas', value: formatNumber(row.chats_used) },
    { label: 'User quotas', value: formatNumber(row.users_used) },
  ];

  return (
    <Panel
      title="Restricted posts per month"
      subtitle="Paid fetches of author-restricted Instagram posts, bot-wide"
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={isAllZero(rows, (row) => row.global_used)}
      emptyText={`No restricted posts fetched in the last ${MONEY_MONTHS} months.`}
      table={{ columns: GATED_COLUMNS, rows, rowKey: (row) => row.month }}
    >
      <ColumnChart
        data={rows}
        xKey="month"
        series={GATED_SERIES}
        stacked={false}
        formatTick={formatMonth}
        formatLabel={formatFullMonth}
        extra={extra}
      />
    </Panel>
  );
}

function StarsMonthly({ query }: { query: Query<MoneyResponse> }) {
  const rows = query.data?.stars_monthly ?? [];
  const extra = (row: StarsMonth) => [
    { label: 'Payments', value: formatNumber(row.payments) },
    { label: 'Boosts', value: formatNumber(row.boosts) },
    { label: 'Refunds', value: formatNumber(row.refunds) },
  ];

  return (
    <Panel
      title="Stars per month"
      subtitle="Refunded payments are left out of Stars received and counted in the month of the refund"
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={isAllZero(rows, (row) => row.stars + row.refunded_stars)}
      emptyText={`No Stars payments in the last ${MONEY_MONTHS} months.`}
      table={{ columns: STARS_COLUMNS, rows, rowKey: (row) => row.month }}
    >
      <ColumnChart
        data={rows}
        xKey="month"
        series={STARS_SERIES}
        stacked={false}
        formatTick={formatMonth}
        formatLabel={formatFullMonth}
        extra={extra}
      />
    </Panel>
  );
}

function GatedThisMonth({ query }: { query: Query<MoneyResponse> }) {
  const rows = query.data?.gated_current ?? [];

  return (
    <Panel
      title="Restricted posts this month"
      subtitle="Per group and user quota. A default limit comes from the bot’s config and is not shown here."
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={rows.length === 0}
      emptyText="No restricted posts fetched this month."
      wide
    >
      <DataTable columns={CURRENT_COLUMNS} rows={rows} rowKey={(row) => `${row.scope}:${row.scope_id}`} caption="Restricted posts this month" />
    </Panel>
  );
}
