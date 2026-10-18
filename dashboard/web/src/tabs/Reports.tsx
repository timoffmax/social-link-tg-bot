import { getReports, REPORTS_LIMIT, REPORTS_WEEKS, type Report, type ReportWeek, type ReportsResponse } from '../api';
import { ClampedText, StatTile, TileGroup } from '../components/Bits';
import { ColumnChart, SERIES_1, SERIES_2, type Series } from '../components/Charts';
import type { Column } from '../components/DataTable';
import { Panel } from '../components/Panel';
import { chatKind, chatLabel, formatDay, formatNumber, formatRelative, formatTimestamp, formatWeek, isAllZero, userLabel } from '../format';
import { useApi, type Query } from '../hooks';
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

export function ReportsTab({ tick, onLoaded }: TabProps) {
  const query = useApi((signal) => getReports(REPORTS_LIMIT, REPORTS_WEEKS, signal).then(onLoaded), [tick]);
  const totals = query.data?.totals ?? null;

  return (
    <div className="grid">
      <TileGroup label="Report totals" loading={query.loading} error={(totals === null) ? query.error : null}>
        <StatTile label="Open reports" value={totals?.open ?? null} />
        <StatTile label="Resolved reports" value={totals?.resolved ?? null} />
      </TileGroup>
      <Weekly query={query} />
      <Latest query={query} />
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

function ReportItem({ report, data }: { report: Report; data: ReportsResponse }) {
  return (
    <li className="report">
      <div className="report__head">
        <span className={report.resolved ? 'report__state report__state--resolved' : 'report__state'}>
          <span aria-hidden="true">{report.resolved ? '✓' : '●'}</span>
          {report.resolved ? 'Resolved' : 'Open'}
        </span>
        <span className="report__id">#{report.id}</span>
        <time dateTime={report.created_at ?? undefined} title={formatTimestamp(report.created_at, data.timezone)}>
          {formatRelative(report.created_at, data.generated_at)}
        </time>
      </div>
      <div className="report__who">{reporterName(report)} {whereSent(report)}</div>
      <ClampedText text={report.message} />
    </li>
  );
}

function Latest({ query }: { query: Query<ReportsResponse> }) {
  const data = query.data;
  const rows = data?.latest ?? [];

  return (
    <Panel
      title="Latest reports"
      subtitle={`Newest first, up to ${REPORTS_LIMIT}`}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={rows.length === 0}
      emptyText="Nobody has reported a problem yet."
      wide
    >
      {(data !== null) && (
        <ul className="reports">
          {rows.map((report) => <ReportItem key={report.id} report={report} data={data} />)}
        </ul>
      )}
    </Panel>
  );
}
