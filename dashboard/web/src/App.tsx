import { useCallback, useEffect, useRef, useState, type ComponentType } from 'react';
import { RANGE_DAYS, type RangeDays, type ResponseMeta } from './api';
import { Segmented, type SegmentOption } from './components/Bits';
import { Tabs } from './components/Tabs';
import { formatClock } from './format';
import { TABS, useHashRoute, useRefreshTick, useTheme, type PostRef, type Tab, type ThemeChoice } from './hooks';
import { BlogTab } from './tabs/Blog';
import { ChatsTab } from './tabs/Chats';
import { MoneyTab } from './tabs/Money';
import { OverviewTab } from './tabs/Overview';
import { PlatformsTab } from './tabs/Platforms';
import { ReportsTab } from './tabs/Reports';
import type { TabProps } from './tabs/types';

interface TabSpec {
  label: string;
  component: ComponentType<TabProps>;
  /** Fixed scope for endpoints that take no days range. */
  fixedScope?: string;
}

const TAB_SPECS: Record<Tab, TabSpec> = {
  overview: { label: 'Overview', component: OverviewTab },
  platforms: { label: 'Platforms', component: PlatformsTab },
  chats: { label: 'Chats', component: ChatsTab },
  money: { label: 'Money', component: MoneyTab, fixedScope: 'Last 12 months' },
  reports: { label: 'Reports', component: ReportsTab, fixedScope: 'Last 26 weeks' },
  blog: { label: 'Blog', component: BlogTab, fixedScope: 'Posts for the bot’s owner-panel Broadcast' },
};

const RANGE_OPTIONS: SegmentOption<RangeDays>[] = RANGE_DAYS.map((days) => ({ value: days, label: `${days} d` }));

const THEME_LABEL: Record<ThemeChoice, string> = {
  auto: 'Theme: system',
  light: 'Theme: light',
  dark: 'Theme: dark',
};

export function App() {
  const [isDirty, setDirty] = useState(false);
  const router = useHashRoute(isDirty);
  const { route, navigate } = router;
  const [tick, refresh] = useRefreshTick();
  const [theme, cycleTheme] = useTheme();
  const [meta, setMeta] = useState<ResponseMeta | null>(null);
  const spec = TAB_SPECS[route.tab];
  const ActiveTab = spec.component;
  const tabLinks = TABS.map((tab) => ({ id: tab, label: TAB_SPECS[tab].label, href: `#/${tab}?days=${route.days}` }));

  const onLoaded = useCallback(<T extends ResponseMeta>(response: T): T => {
    setMeta({ generated_at: response.generated_at, timezone: response.timezone });

    return response;
  }, []);

  const openPost = useCallback((post: PostRef | null, force?: boolean) => {
    navigate({ tab: 'blog', days: route.days, post }, force);
  }, [navigate, route.days]);

  return (
    <div className="app">
      <header className="masthead">
        <div className="masthead__brand">
          <h1 className="masthead__title">Social Link Bot</h1>
          <p className="masthead__meta">
            {(meta === null) ? 'Loading…' : <>Updated {formatClock(meta.generated_at, meta.timezone)} <span className="tz">{meta.timezone}</span></>}
          </p>
        </div>
        <div className="masthead__actions">
          <button type="button" className="button" onClick={refresh}>Refresh</button>
          <button type="button" className="button button--quiet" onClick={cycleTheme} aria-label={`${THEME_LABEL[theme]}. Change theme`}>
            {THEME_LABEL[theme]}
          </button>
        </div>
      </header>

      <Tabs tabs={tabLinks} active={route.tab} />

      {(router.pending !== null) && <LeavePrompt onLeave={router.leave} onStay={router.stay} />}

      <div className="filters">
        {(spec.fixedScope === undefined) && (
          <Segmented
            label="Date range"
            options={RANGE_OPTIONS}
            value={route.days}
            onChange={(days) => navigate({ tab: route.tab, days, post: null })}
          />
        )}
        {(spec.fixedScope !== undefined) && <span className="filters__scope">{spec.fixedScope}</span>}
      </div>

      <main className="content">
        <ActiveTab
          key={route.tab}
          days={route.days}
          tick={tick}
          onLoaded={onLoaded}
          post={route.post}
          openPost={openPost}
          onDirtyChange={setDirty}
        />
      </main>
    </div>
  );
}

interface LeavePromptProps {
  onLeave: () => void;
  onStay: () => void;
}

/** Shown when a navigation would drop unsaved post edits. */
function LeavePrompt({ onLeave, onStay }: LeavePromptProps) {
  const stayButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    stayButton.current?.focus();
  }, []);

  return (
    <div className="leave-prompt" role="alertdialog" aria-labelledby="leave-prompt-text">
      <p id="leave-prompt-text">This post has unsaved changes. Leaving discards them.</p>
      <div className="leave-prompt__actions">
        <button ref={stayButton} type="button" className="button" onClick={onStay}>Keep editing</button>
        <button type="button" className="button button--danger" onClick={onLeave}>Discard changes</button>
      </div>
    </div>
  );
}
