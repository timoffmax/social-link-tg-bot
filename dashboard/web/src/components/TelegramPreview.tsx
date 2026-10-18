import { Fragment, useState, type ReactNode } from 'react';
import { safeHref } from '../editing';
import type { TgElement, TgNode, TgTag } from '../telegramHtml';

// Renders parsed Telegram HTML as React elements from a fixed whitelist: the
// parser only produces the tags below, text is passed as text (React escapes
// it), and no attribute other than a checked href ever reaches the DOM.

type Render = (element: TgElement, key: number) => ReactNode;

const RENDERERS: Record<TgTag, Render> = {
  b: (element, key) => <strong key={key}>{renderNodes(element.children)}</strong>,
  strong: (element, key) => <strong key={key}>{renderNodes(element.children)}</strong>,
  i: (element, key) => <em key={key}>{renderNodes(element.children)}</em>,
  em: (element, key) => <em key={key}>{renderNodes(element.children)}</em>,
  u: (element, key) => <u key={key}>{renderNodes(element.children)}</u>,
  ins: (element, key) => <u key={key}>{renderNodes(element.children)}</u>,
  s: (element, key) => <s key={key}>{renderNodes(element.children)}</s>,
  strike: (element, key) => <s key={key}>{renderNodes(element.children)}</s>,
  del: (element, key) => <s key={key}>{renderNodes(element.children)}</s>,
  span: (element, key) => <Spoiler key={key}>{renderNodes(element.children)}</Spoiler>,
  'tg-spoiler': (element, key) => <Spoiler key={key}>{renderNodes(element.children)}</Spoiler>,
  a: (element, key) => <PreviewLink key={key} href={element.attrs.href ?? ''}>{renderNodes(element.children)}</PreviewLink>,
  code: (element, key) => <code key={key} className="tg-code">{renderNodes(element.children)}</code>,
  pre: (element, key) => <pre key={key} className="tg-pre">{renderNodes(element.children)}</pre>,
  blockquote: (element, key) => (
    <Quote key={key} expandable={('expandable' in element.attrs)}>{renderNodes(element.children)}</Quote>
  ),
  'tg-emoji': (element, key) => <span key={key} className="tg-emoji">{renderNodes(element.children)}</span>,
};

function renderNode(node: TgNode, key: number): ReactNode {
  const result = (node.kind === 'text') ? <Fragment key={key}>{node.text}</Fragment> : RENDERERS[node.tag](node, key);

  return result;
}

export function renderNodes(nodes: TgNode[]): ReactNode[] {
  return nodes.map((node, index) => renderNode(node, index));
}

function Spoiler({ children }: { children: ReactNode }) {
  const [revealed, setRevealed] = useState(false);
  const toggle = () => setRevealed((value) => (value === false));

  return (
    <span
      className={revealed ? 'tg-spoiler is-revealed' : 'tg-spoiler'}
      role="button"
      tabIndex={0}
      aria-pressed={revealed}
      title={revealed ? 'Hide spoiler' : 'Show spoiler'}
      onClick={toggle}
      onKeyDown={(event) => {
        const isActivation = (event.key === 'Enter') || (event.key === ' ');

        if (isActivation) {
          event.preventDefault();
          toggle();
        }
      }}
    >
      {children}
    </span>
  );
}

function PreviewLink({ href, children }: { href: string; children: ReactNode }) {
  const safe = safeHref(href);
  const inert = <span className="tg-link tg-link--inert" title={`Not a full link: "${href}"`}>{children}</span>;

  return (safe === null) ? inert : (
    <a className="tg-link" href={safe} target="_blank" rel="noopener noreferrer" title={safe}>{children}</a>
  );
}

function Quote({ expandable, children }: { expandable: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const isCollapsed = expandable && (open === false);

  return (
    <blockquote className={isCollapsed ? 'tg-quote tg-quote--collapsed' : 'tg-quote'}>
      {children}
      {expandable && (
        <button
          type="button"
          className="tg-quote__toggle"
          aria-expanded={open}
          onClick={() => setOpen((value) => (value === false))}
        >
          {open ? 'Collapse' : 'Expand'}
        </button>
      )}
    </blockquote>
  );
}

interface TelegramPreviewProps {
  nodes: TgNode[];
  /** The content has a problem: the bubble shows the last version that parsed. */
  stale: boolean;
  time: string;
}

/** The post as a Telegram message bubble. */
export function TelegramPreview({ nodes, stale, time }: TelegramPreviewProps) {
  const isEmpty = (nodes.length === 0);

  return (
    <div className="tg-chat">
      <div className={stale ? 'tg-bubble is-stale' : 'tg-bubble'}>
        {isEmpty && <p className="tg-bubble__placeholder">The message preview appears here as you type.</p>}
        {(isEmpty === false) && <div className="tg-bubble__text">{renderNodes(nodes)}</div>}
        <span className="tg-bubble__time">{time}</span>
      </div>
    </div>
  );
}
