import { useCallback, useEffect, useRef, useState } from 'react';
import { DEFAULT_RANGE_DAYS, RANGE_DAYS, type RangeDays } from './api';

export const TABS = ['overview', 'platforms', 'chats', 'money', 'reports', 'blog'] as const;
export type Tab = (typeof TABS)[number];

const DEFAULT_TAB: Tab = 'overview';
const AUTO_REFRESH_MS = 5 * 60_000;
const THEME_KEY = 'dashboard-theme';
const NEW_POST = 'new';
const POST_ID = /^[1-9][0-9]*$/;

/** The post open in the Blog editor: a saved post's id or a new one. */
export type PostRef = number | typeof NEW_POST;

export interface Route {
  tab: Tab;
  days: RangeDays;
  post: PostRef | null;
}

function isTab(value: string): value is Tab {
  return (TABS as readonly string[]).includes(value);
}

function isRange(value: number): value is RangeDays {
  return (RANGE_DAYS as readonly number[]).includes(value);
}

function parsePost(value: string | null): PostRef | null {
  let result: PostRef | null = null;

  if (value === NEW_POST) {
    result = NEW_POST;
  }

  if ((value !== null) && POST_ID.test(value)) {
    result = Number(value);
  }

  return result;
}

/** "#/platforms?days=30" -> { tab: "platforms", days: 30, post: null }; unknown parts fall back to defaults. */
export function parseHash(hash: string): Route {
  const [path = '', search = ''] = hash.replace(/^#\/?/, '').split('?');
  const params = new URLSearchParams(search);
  const days = Number(params.get('days'));

  return {
    tab: isTab(path) ? path : DEFAULT_TAB,
    days: isRange(days) ? days : DEFAULT_RANGE_DAYS,
    post: parsePost(params.get('post')),
  };
}

export function buildHash(route: Route): string {
  const post = (route.post === null) ? '' : `&post=${route.post}`;

  return `#/${route.tab}?days=${route.days}${post}`;
}

export interface HashRouter {
  route: Route;
  /** force skips the unsaved-changes guard (after a save or a confirmed discard). */
  navigate: (next: Route, force?: boolean) => void;
  /** A navigation held back by the guard, waiting for leave() or stay(). */
  pending: Route | null;
  leave: () => void;
  stay: () => void;
}

/**
 * Hash routing with an unsaved-changes guard: while isBlocked, a hash change
 * (tab link, back button, a link in the page) is undone and kept as pending
 * until the user confirms leaving.
 */
export function useHashRoute(isBlocked: boolean): HashRouter {
  const [route, setRoute] = useState<Route>(() => parseHash(window.location.hash));
  const [pending, setPending] = useState<Route | null>(null);
  const blocked = useRef(isBlocked);
  const force = useRef(false);
  const acceptedHash = useRef(window.location.hash);

  useEffect(() => {
    blocked.current = isBlocked;
  }, [isBlocked]);

  useEffect(() => {
    const onChange = () => {
      const next = parseHash(window.location.hash);
      const mayLeave = force.current || (blocked.current === false);
      force.current = false;

      if (mayLeave) {
        acceptedHash.current = window.location.hash;
        setRoute(next);
        setPending(null);
      }

      if (mayLeave === false) {
        window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}${acceptedHash.current}`);
        setPending(next);
      }
    };
    window.addEventListener('hashchange', onChange);

    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  const navigate = useCallback((next: Route, isForced = false) => {
    const hash = buildHash(next);

    if (hash !== window.location.hash) {
      force.current = isForced;
      window.location.hash = hash;
    }
  }, []);

  const leave = useCallback(() => {
    if (pending !== null) {
      navigate(pending, true);
    }
  }, [navigate, pending]);

  const stay = useCallback(() => setPending(null), []);

  return { route, navigate, pending, leave, stay };
}

/**
 * A counter that bumps every five minutes and on demand; data hooks refetch
 * when it changes. A tab that was hidden past the interval refreshes when shown.
 */
export function useRefreshTick(): [number, () => void] {
  const [tick, setTick] = useState(0);
  const lastRefresh = useRef(Date.now());

  const refresh = useCallback(() => {
    lastRefresh.current = Date.now();
    setTick((value) => value + 1);
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        refresh();
      }
    }, AUTO_REFRESH_MS);
    const onVisible = () => {
      const isStale = (Date.now() - lastRefresh.current) >= AUTO_REFRESH_MS;

      if ((document.visibilityState === 'visible') && isStale) {
        refresh();
      }
    };
    document.addEventListener('visibilitychange', onVisible);

    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [refresh]);

  return [tick, refresh];
}

export interface Query<T> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

function errorMessage(error: unknown): string {
  const result = (error instanceof Error) ? error.message : 'Unknown error';

  return result;
}

/**
 * Fetches on mount and whenever a key changes. Keeps the previous data while
 * a refetch runs, so charts hold their frame instead of flashing.
 */
export function useApi<T>(load: (signal: AbortSignal) => Promise<T>, keys: unknown[]): Query<T> {
  const [state, setState] = useState<Query<T>>({ data: null, error: null, loading: true });

  useEffect(() => {
    const controller = new AbortController();
    setState((previous) => ({ ...previous, loading: true }));

    load(controller.signal)
      .then((data) => setState({ data, error: null, loading: false }))
      .catch((error: unknown) => {
        const isAbort = (error instanceof DOMException) && (error.name === 'AbortError');

        if (isAbort === false) {
          setState((previous) => ({ data: previous.data, error: errorMessage(error), loading: false }));
        }
      });

    return () => controller.abort();
    // The loader is recreated every render; the keys decide when to refetch.
  }, keys);

  return state;
}

export type ThemeChoice = 'auto' | 'light' | 'dark';

function readTheme(): ThemeChoice {
  let result: ThemeChoice = 'auto';

  try {
    const stored = window.localStorage.getItem(THEME_KEY);

    if ((stored === 'light') || (stored === 'dark')) {
      result = stored;
    }
  } catch {
    // Storage blocked: follow the system theme.
  }

  return result;
}

export function useTheme(): [ThemeChoice, () => void] {
  const [theme, setTheme] = useState<ThemeChoice>(readTheme);

  useEffect(() => {
    const root = document.documentElement;

    if (theme === 'auto') {
      root.removeAttribute('data-theme');
    }

    if (theme !== 'auto') {
      root.setAttribute('data-theme', theme);
    }

    try {
      window.localStorage.setItem(THEME_KEY, theme);
    } catch {
      // Storage blocked: the choice lasts for this visit only.
    }
  }, [theme]);

  const cycle = useCallback(() => {
    const next: Record<ThemeChoice, ThemeChoice> = { auto: 'light', light: 'dark', dark: 'auto' };
    setTheme((value) => next[value]);
  }, []);

  return [theme, cycle];
}
