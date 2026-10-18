// Pure helpers for the blog post editor (no imports, so `node --test` runs them as is).

export const TITLE_MAX_CHARS = 255;

export interface FormatTool {
  id: string;
  /** Button text; the button's style shows the effect where it can. */
  label: string;
  /** Accessible name and tooltip. */
  title: string;
  open: string;
  close: string;
  /** Where the caret goes after wrapping: around the wrapped text or into the empty href. */
  caret: 'inner' | 'href';
}

export const FORMAT_TOOLS: FormatTool[] = [
  { id: 'bold', label: 'B', title: 'Bold', open: '<b>', close: '</b>', caret: 'inner' },
  { id: 'italic', label: 'I', title: 'Italic', open: '<i>', close: '</i>', caret: 'inner' },
  { id: 'underline', label: 'U', title: 'Underline', open: '<u>', close: '</u>', caret: 'inner' },
  { id: 'strike', label: 'S', title: 'Strikethrough', open: '<s>', close: '</s>', caret: 'inner' },
  { id: 'code', label: 'Code', title: 'Inline code', open: '<code>', close: '</code>', caret: 'inner' },
  { id: 'link', label: 'Link', title: 'Link (type the address between the quotes)', open: '<a href="">', close: '</a>', caret: 'href' },
  { id: 'spoiler', label: 'Spoiler', title: 'Spoiler', open: '<tg-spoiler>', close: '</tg-spoiler>', caret: 'inner' },
  { id: 'quote', label: 'Quote', title: 'Quote', open: '<blockquote>', close: '</blockquote>', caret: 'inner' },
];

const HREF_CARET_OFFSET = '<a href="'.length;

export interface Wrapped {
  /** The text that replaces the selection. */
  insert: string;
  /** The selection to restore afterwards, as absolute offsets in the new value. */
  selectionStart: number;
  selectionEnd: number;
}

/** Wraps value[start, end) in the tool's tags; the caller replaces that range with insert. */
export function wrapSelection(value: string, start: number, end: number, tool: FormatTool): Wrapped {
  const selected = value.slice(start, end);
  const innerStart = start + tool.open.length;
  const innerEnd = innerStart + selected.length;
  const isHref = (tool.caret === 'href');
  const caret = start + HREF_CARET_OFFSET;

  return {
    insert: `${tool.open}${selected}${tool.close}`,
    selectionStart: isHref ? caret : innerStart,
    selectionEnd: isHref ? caret : innerEnd,
  };
}

/** VARCHAR(255) counts characters (code points) of the trimmed title, like the server. */
export function titleLength(title: string): number {
  return [...title.trim()].length;
}

const SAFE_PROTOCOLS = ['http:', 'https:', 'tg:', 'mailto:'];

/** A link the preview may make clickable; anything else (javascript:, relative) is shown inert. */
export function safeHref(href: string): string | null {
  let result: string | null = null;

  try {
    const url = new URL(href);

    if (SAFE_PROTOCOLS.includes(url.protocol)) {
      result = url.href;
    }
  } catch {
    // Not an absolute URL: Telegram would not link it either.
  }

  return result;
}

export type DraftField = 'title' | 'content';

/** Which field a server validation message is about: title messages start with "title". */
export function errorField(message: string): DraftField {
  const isTitle = message.trim().toLowerCase().startsWith('title');
  const result: DraftField = isTitle ? 'title' : 'content';

  return result;
}

export interface Draft {
  title: string;
  content: string;
}

export function isSameDraft(left: Draft, right: Draft): boolean {
  return (left.title === right.title) && (left.content === right.content);
}
