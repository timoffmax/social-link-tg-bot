import { CHATS_LIMIT, getChats, type ChatsResponse, type CustomSettingsChat, type TopGroup, type TopUser } from '../api';
import { StatTile, TileGroup } from '../components/Bits';
import { DataTable, type Column } from '../components/DataTable';
import { Panel } from '../components/Panel';
import {
  chatKind,
  chatLabel,
  formatCount,
  formatFullMonth,
  formatNumber,
  formatRelative,
  formatTimestamp,
  isSettingOn,
  platformLabel,
  retentionRate,
  userLabel,
} from '../format';
import { useApi, type Query } from '../hooks';
import type { TabProps } from './types';

function lastSeen(timestamp: string | null, data: ChatsResponse) {
  return <time dateTime={timestamp ?? undefined} title={formatTimestamp(timestamp, data.timezone)}>{formatRelative(timestamp, data.generated_at)}</time>;
}

function groupColumns(data: ChatsResponse): Column<TopGroup>[] {
  return [
    { header: 'Group', cell: (row) => <span className="cell-title">{chatLabel(row.title, row.chat_id)}</span> },
    { header: 'Downloads', cell: (row) => formatNumber(row.downloads), numeric: true },
    { header: 'Users', cell: (row) => formatNumber(row.users), numeric: true },
    { header: 'Last', cell: (row) => lastSeen(row.last_at, data) },
  ];
}

function userColumns(data: ChatsResponse): Column<TopUser>[] {
  return [
    { header: 'User', cell: (row) => <span className="cell-title">{userLabel(row.username, row.user_id)}</span> },
    { header: 'Downloads', cell: (row) => formatNumber(row.downloads), numeric: true },
    { header: 'Last', cell: (row) => lastSeen(row.last_at, data) },
  ];
}

export function ChatsTab({ days, tick, onLoaded }: TabProps) {
  const query = useApi((signal) => getChats(days, CHATS_LIMIT, signal).then(onLoaded), [days, tick]);

  return (
    <div className="grid">
      <RetentionTiles query={query} />
      <TopGroups query={query} days={days} />
      <TopUsers query={query} days={days} />
      <CustomSettings query={query} />
    </div>
  );
}

function RetentionTiles({ query }: { query: Query<ChatsResponse> }) {
  const retention = query.data?.retention ?? null;
  const monthName = (retention === null) ? 'this month' : formatFullMonth(retention.month);

  return (
    <TileGroup label="Monthly retention" loading={query.loading} error={(retention === null) ? query.error : null}>
      <StatTile label={`Active chats, ${monthName}`} value={retention?.active_this_month ?? null} />
      <StatTile label="Active chats, month before" value={retention?.active_last_month ?? null} />
      <StatTile label="Active in both months" value={retention?.retained ?? null} />
      <StatTile
        label="Came back this month"
        value={null}
        display={(retention === null) ? '–' : retentionRate(retention.retained, retention.active_last_month)}
        note="of last month’s active chats"
      />
    </TileGroup>
  );
}

function TopGroups({ query, days }: { query: Query<ChatsResponse>; days: number }) {
  const data = query.data;
  const rows = data?.top_groups ?? [];
  const shownDays = data?.days ?? days;

  return (
    <Panel
      title="Top groups"
      subtitle={`By downloads in the last ${shownDays} days`}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={rows.length === 0}
      emptyText="No group downloads in this range."
    >
      {(data !== null) && <DataTable columns={groupColumns(data)} rows={rows} rowKey={(row) => row.chat_id} caption="Top groups" />}
    </Panel>
  );
}

function TopUsers({ query, days }: { query: Query<ChatsResponse>; days: number }) {
  const data = query.data;
  const rows = data?.top_users ?? [];
  const shownDays = data?.days ?? days;

  return (
    <Panel
      title="Top private users"
      subtitle={`By downloads in private chats, last ${shownDays} days`}
      loading={query.loading}
      error={query.error}
      hasData={data !== null}
      isEmpty={rows.length === 0}
      emptyText="No private-chat downloads in this range."
    >
      {(data !== null) && <DataTable columns={userColumns(data)} rows={rows} rowKey={(row) => row.user_id} caption="Top private users" />}
    </Panel>
  );
}

function settingLabel(type: string, key: string): string {
  const result = (type === 'platform') ? platformLabel(key) : `${type}: ${key}`;

  return result;
}

function SettingsCard({ chat }: { chat: CustomSettingsChat }) {
  const kind = chatKind(chat.chat_id);

  return (
    <li className="settings-card">
      <div className="settings-card__head">
        <span className="cell-title">{chatLabel(chat.title, chat.chat_id)}</span>
        <span className="kind">{(kind === 'group') ? 'Group' : 'Private'}</span>
      </div>
      <dl className="facts">
        <div><dt>Daily limit</dt><dd>{(chat.daily_limit > 0) ? formatNumber(chat.daily_limit) : 'Default'}</dd></div>
        <div><dt>Timezone</dt><dd>{chat.timezone}</dd></div>
        <div><dt>Per-user limits</dt><dd>{formatNumber(chat.user_limits)}</dd></div>
      </dl>
      {(chat.settings.length > 0) && (
        <ul className="chips" aria-label="Settings">
          {chat.settings.map((setting) => {
            const isOn = isSettingOn(setting);

            return (
              <li key={`${setting.type}:${setting.key}`} className={isOn ? 'chip' : 'chip chip--off'} title={`Value: ${setting.value}`}>
                <span aria-hidden="true">{isOn ? '✓' : '✕'}</span>
                {settingLabel(setting.type, setting.key)}
                <span className="visually-hidden">{isOn ? ' on' : ' off'}</span>
              </li>
            );
          })}
        </ul>
      )}
    </li>
  );
}

function CustomSettings({ query }: { query: Query<ChatsResponse> }) {
  const rows = query.data?.custom_settings ?? [];

  return (
    <Panel
      title="Chats with custom settings or limits"
      subtitle={(query.data === null) ? undefined : `${formatCount(rows.length, 'chat', 'chats')} with a setting changed from its default or a limit. Only the changed settings are shown.`}
      loading={query.loading}
      error={query.error}
      hasData={query.data !== null}
      isEmpty={rows.length === 0}
      emptyText="No chat has custom settings or limits."
      wide
    >
      <ul className="settings-list">
        {rows.map((chat) => <SettingsCard key={chat.chat_id} chat={chat} />)}
      </ul>
    </Panel>
  );
}
