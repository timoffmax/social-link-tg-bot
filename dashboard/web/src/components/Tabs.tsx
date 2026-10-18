import { useCallback, useLayoutEffect, useRef, useState, type FocusEvent } from 'react';
import { useResizeObserver } from '../hooks';

export interface TabLink {
  id: string;
  label: string;
  href: string;
}

interface TabsProps {
  tabs: TabLink[];
  active: string;
}

interface Overflow {
  start: boolean;
  end: boolean;
}

/** Room kept beside the active tab when it is scrolled into view. */
const SCROLL_MARGIN = 24;
/** Sub-pixel slack, so a fully scrolled row does not keep its fade. */
const EDGE_SLACK = 1;

function readOverflow(nav: HTMLElement): Overflow {
  const maxScroll = nav.scrollWidth - nav.clientWidth;

  return {
    start: (nav.scrollLeft > EDGE_SLACK),
    end: (nav.scrollLeft < (maxScroll - EDGE_SLACK)),
  };
}

/** Horizontal scroll that brings the item fully into the row, or 0 when it is visible. */
function scrollDelta(nav: HTMLElement, item: HTMLElement): number {
  const navBox = nav.getBoundingClientRect();
  const itemBox = item.getBoundingClientRect();
  let result = 0;

  if (itemBox.right > navBox.right) {
    result = (itemBox.right - navBox.right) + SCROLL_MARGIN;
  }

  if (itemBox.left < navBox.left) {
    result = (itemBox.left - navBox.left) - SCROLL_MARGIN;
  }

  return result;
}

/**
 * The section tabs. When they do not fit (the narrowest phones), the row
 * scrolls on its own, keeps the active tab in view and fades the edge that
 * hides more tabs.
 */
export function Tabs({ tabs, active }: TabsProps) {
  const nav = useRef<HTMLElement>(null);
  const [overflow, setOverflow] = useState<Overflow>({ start: false, end: false });

  const update = useCallback(() => {
    const element = nav.current;

    if (element !== null) {
      const next = readOverflow(element);
      setOverflow(next);
    }
  }, []);

  // Keyboard focus on a tab hidden past the edge brings it into the row.
  const reveal = useCallback((event: FocusEvent<HTMLElement>) => {
    const element = nav.current;
    const item = event.target;

    if ((element !== null) && (item instanceof HTMLElement)) {
      element.scrollLeft += scrollDelta(element, item);
    }

    update();
  }, [update]);

  useLayoutEffect(() => {
    const element = nav.current;
    const item = element?.querySelector<HTMLElement>('[aria-current="page"]') ?? null;

    if ((element !== null) && (item !== null)) {
      element.scrollLeft += scrollDelta(element, item);
    }

    update();
  }, [active, update]);

  useResizeObserver(nav, update);

  return (
    <div className="tabs-row" data-fade-start={overflow.start} data-fade-end={overflow.end}>
      <nav ref={nav} className="tabs" aria-label="Dashboard sections" onScroll={update} onFocus={reveal}>
        {tabs.map((tab) => (
          <a
            key={tab.id}
            href={tab.href}
            className="tabs__item"
            aria-current={(tab.id === active) ? 'page' : undefined}
          >
            {tab.label}
          </a>
        ))}
      </nav>
    </div>
  );
}
