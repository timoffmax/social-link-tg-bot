// Parser for the Telegram HTML subset a blog post may use (parse_mode HTML).
// Pure and dependency-free, so it runs in the browser and under `node --test`
// (only erasable TypeScript syntax: no enums, no parameter properties).
//
// It mirrors the server check (dashboard/telegram_html.go): a whitelist of
// tags and attributes, every tag closed and properly nested, and `<`, `>`, `&`
// escaped outside tags. Entities: &amp; &lt; &gt; &quot; and numeric
// references, as Telegram decodes them. The length is what Telegram counts: the text with tags
// removed and entities decoded, in UTF-16 code units, of the trimmed content.

export const TELEGRAM_TEXT_LIMIT = 4096;

/**
 * The text the bot's owner panel puts around a post on its Broadcast preview, the screen with the send
 * buttons (showBroadcastPreview in app/updates.go; tags removed), with seven-digit counts for headroom.
 * Mirrors broadcastPreviewFrame in dashboard/blog_handlers.go.
 */
const BROADCAST_PREVIEW_FRAME = '📢 Broadcast Preview\n\n━━━━━━━━━━━━━━━━━━━━\n\n\n\n━━━━━━━━━━━━━━━━━━━━\n\n👤 Individual users: 9999999\n👥 Groups: 9999999\n\nChoose audience:';

/** UTF-16 length of the Broadcast preview's own text. */
export const BROADCAST_FRAME_LENGTH = BROADCAST_PREVIEW_FRAME.length;

/** The longest content whose Broadcast preview still fits Telegram's limit; the server enforces it too. */
export const BROADCAST_CONTENT_LIMIT = TELEGRAM_TEXT_LIMIT - BROADCAST_FRAME_LENGTH;

export type TgTag =
  | 'b' | 'strong' | 'i' | 'em' | 'u' | 'ins' | 's' | 'strike' | 'del'
  | 'span' | 'tg-spoiler' | 'a' | 'code' | 'pre' | 'blockquote' | 'tg-emoji';

export interface TgText {
  kind: 'text';
  text: string;
}

export interface TgElement {
  kind: 'element';
  tag: TgTag;
  attrs: Record<string, string>;
  children: TgNode[];
}

export type TgNode = TgText | TgElement;

export interface TgProblem {
  message: string;
  /** UTF-16 index into the original (untrimmed) content, as textarea selections use. */
  offset: number;
  /** 1-based. */
  line: number;
  /** 1-based, in characters. */
  column: number;
}

export interface TgParseResult {
  nodes: TgNode[];
  /** Telegram length of the parsed text (meaningful when there is no problem). */
  length: number;
  problem: TgProblem | null;
}

type AttrRule = (attrs: Record<string, string>) => string | null;

const NO_ATTRS: AttrRule = (attrs) => noAttributes(attrs);

const TAG_RULES: Record<TgTag, AttrRule> = {
  b: NO_ATTRS,
  strong: NO_ATTRS,
  i: NO_ATTRS,
  em: NO_ATTRS,
  u: NO_ATTRS,
  ins: NO_ATTRS,
  s: NO_ATTRS,
  strike: NO_ATTRS,
  del: NO_ATTRS,
  'tg-spoiler': NO_ATTRS,
  pre: NO_ATTRS,
  span: (attrs) => spanAttributes(attrs),
  a: (attrs) => onlyAttributes(attrs, ['href']),
  code: (attrs) => codeAttributes(attrs),
  blockquote: (attrs) => onlyAttributes(attrs, [EXPANDABLE_ATTR]),
  'tg-emoji': (attrs) => emojiAttributes(attrs),
};

const ENTITIES: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"' };
const SPOILER_CLASS = 'tg-spoiler';
const CODE_CLASS_PREFIX = 'language-';
const EXPANDABLE_ATTR = 'expandable';
const EMOJI_ID_ATTR = 'emoji-id';

// Whitespace inside tags is ASCII only ([ \t\n\r\f]), like the server: a
// no-break space there is not a separator.
const OPEN_TAG = /<([A-Za-z][A-Za-z0-9-]*)((?:[ \t\n\r\f]+[^ \t\n\r\f=>/"']+(?:[ \t\n\r\f]*=[ \t\n\r\f]*(?:"[^"]*"|'[^']*'|[^ \t\n\r\f"'>]+))?)*)[ \t\n\r\f]*>/y;
const CLOSE_TAG = /<\/([A-Za-z][A-Za-z0-9-]*)[ \t\n\r\f]*>/y;
const ENTITY = /&(amp|lt|gt|quot|#[0-9]+|#[xX][0-9A-Fa-f]+);/y;
const ENTITY_ANYWHERE = /&(amp|lt|gt|quot|#[0-9]+|#[xX][0-9A-Fa-f]+);/g;
const HEX_REFERENCE = /^#[xX]/;
const DECIMAL_BASE = 10;
const HEX_BASE = 16;
const HEX_DIGITS_START = 2;
const DECIMAL_DIGITS_START = 1;
const MAX_CODE_POINT = 0x10FFFF;
const SURROGATE_MIN = 0xD800;
const SURROGATE_MAX = 0xDFFF;
const ATTRIBUTE = /([^ \t\n\r\f=>/"']+)(?:[ \t\n\r\f]*=[ \t\n\r\f]*(?:"([^"]*)"|'([^']*)'|([^ \t\n\r\f"'>]+)))?/g;
const EMOJI_ID = /^[0-9]+$/;
const TAG_START = /<\/?[A-Za-z]/y;
const BARE_LT = 'A bare "<" must be written as &lt;';
const MALFORMED_TAG = 'Malformed tag: check its quotes and the closing ">"';

function isTag(name: string): name is TgTag {
  return Object.prototype.hasOwnProperty.call(TAG_RULES, name);
}

function noAttributes(attrs: Record<string, string>): string | null {
  const names = Object.keys(attrs);
  const result = (names.length === 0) ? null : `has an attribute "${names[0]}" Telegram does not allow`;

  return result;
}

function onlyAttributes(attrs: Record<string, string>, allowed: string[]): string | null {
  const extra = Object.keys(attrs).filter((name) => (allowed.includes(name) === false));
  const result = (extra.length === 0) ? null : `has an attribute "${extra[0]}" Telegram does not allow`;

  return result;
}

function spanAttributes(attrs: Record<string, string>): string | null {
  let result = onlyAttributes(attrs, ['class']);

  if ((result === null) && (attrs.class !== SPOILER_CLASS)) {
    result = `needs class="${SPOILER_CLASS}" (Telegram only supports spoiler spans)`;
  }

  return result;
}

function codeAttributes(attrs: Record<string, string>): string | null {
  let result = onlyAttributes(attrs, ['class']);
  const className = attrs.class;

  if ((result === null) && (className !== undefined) && (className.startsWith(CODE_CLASS_PREFIX) === false)) {
    result = `class must start with "${CODE_CLASS_PREFIX}"`;
  }

  return result;
}

function emojiAttributes(attrs: Record<string, string>): string | null {
  let result = onlyAttributes(attrs, [EMOJI_ID_ATTR]);
  const emojiId = attrs[EMOJI_ID_ATTR] ?? '';

  if ((result === null) && (EMOJI_ID.test(emojiId) === false)) {
    result = `needs a numeric ${EMOJI_ID_ATTR}`;
  }

  return result;
}

/** A numeric reference ("#39", "#x1F600") as a character; null for zero, surrogates and out-of-range values. */
function numericReference(name: string): string | null {
  const isHex = HEX_REFERENCE.test(name);
  const digits = name.slice(isHex ? HEX_DIGITS_START : DECIMAL_DIGITS_START);
  const value = Number.parseInt(digits, isHex ? HEX_BASE : DECIMAL_BASE);
  const isSurrogate = (value >= SURROGATE_MIN) && (value <= SURROGATE_MAX);
  const isValid = (value > 0) && (value <= MAX_CODE_POINT) && (isSurrogate === false);
  const result = isValid ? String.fromCodePoint(value) : null;

  return result;
}

/** The character an entity name stands for (named or numeric, as Telegram decodes them), or null. */
function decodeEntity(name: string): string | null {
  const named = ENTITIES[name];
  const result = (named !== undefined) ? named : numericReference(name);

  return result;
}

/** Decodes the entities Telegram supports; other `&` stay as they are. */
export function decodeEntities(value: string): string {
  return value.replace(ENTITY_ANYWHERE, (match, name: string) => decodeEntity(name) ?? match);
}

interface ParsedAttributes {
  attrs: Record<string, string>;
  duplicate: string | null;
}

function parseAttributes(source: string): ParsedAttributes {
  const result: ParsedAttributes = { attrs: {}, duplicate: null };

  for (const match of source.matchAll(ATTRIBUTE)) {
    const name = (match[1] ?? '').toLowerCase();
    const raw = match[2] ?? match[3] ?? match[4];

    if ((name in result.attrs) && (result.duplicate === null)) {
      result.duplicate = name;
    }

    result.attrs[name] = decodeEntities(raw ?? '');
  }

  return result;
}

/** 1-based line and column (in characters) of a UTF-16 offset. */
export function locate(source: string, offset: number): { line: number; column: number } {
  const before = source.slice(0, offset);
  const lines = before.split('\n');
  const lastLine = lines[lines.length - 1] ?? '';

  return { line: lines.length, column: [...lastLine].length + 1 };
}

interface Frame {
  element: TgElement;
  offset: number;
}

class Parser {
  readonly source: string;
  readonly root: TgNode[] = [];
  readonly stack: Frame[] = [];
  index: number;
  readonly end: number;
  text = '';
  length = 0;
  problem: TgProblem | null = null;

  constructor(source: string, start: number, end: number) {
    this.source = source;
    this.index = start;
    this.end = end;
  }

  run(): void {
    while ((this.index < this.end) && (this.problem === null)) {
      this.step();
    }

    this.flushText();

    const unclosed = this.stack[this.stack.length - 1];

    if ((this.problem === null) && (unclosed !== undefined)) {
      this.fail(`<${unclosed.element.tag}> is never closed`, unclosed.offset);
    }
  }

  private step(): void {
    const char = this.source[this.index] ?? '';
    const handlers: Record<string, () => void> = {
      '<': () => this.readTag(),
      '>': () => this.fail('A bare ">" must be written as &gt;', this.index),
      '&': () => this.readEntity(),
    };
    const handler = handlers[char];

    if (handler !== undefined) {
      handler();
    }

    if (handler === undefined) {
      this.text += char;
      this.index += 1;
    }
  }

  private matchAt(pattern: RegExp): RegExpExecArray | null {
    pattern.lastIndex = this.index;
    const match = pattern.exec(this.source);
    const fits = (match !== null) && ((this.index + match[0].length) <= this.end);
    const result = fits ? match : null;

    return result;
  }

  private readEntity(): void {
    const match = this.matchAt(ENTITY);
    const decoded = (match === null) ? null : decodeEntity(match[1] ?? '');

    if (match === null) {
      this.fail('A bare "&" must be written as &amp;', this.index);
    }

    if ((match !== null) && (decoded === null)) {
      this.fail(`Invalid character reference ${match[0]}`, this.index);
    }

    if ((match !== null) && (decoded !== null)) {
      this.text += decoded;
      this.index += match[0].length;
    }
  }

  private readTag(): void {
    const isClosing = (this.source[this.index + 1] === '/');

    if (isClosing) {
      this.readClosingTag();
    }

    if (isClosing === false) {
      this.readOpeningTag();
    }
  }

  private readOpeningTag(): void {
    const match = this.matchAt(OPEN_TAG);
    const name = (match?.[1] ?? '').toLowerCase();

    if (match === null) {
      this.failUnmatchedLt();
    }

    if ((match !== null) && (isTag(name) === false)) {
      this.fail(`<${name}> is not supported by Telegram`, this.index);
    }

    if ((match !== null) && isTag(name)) {
      this.openElement(name, match[2] ?? '', match[0].length);
    }
  }

  private openElement(tag: TgTag, attributeSource: string, tagLength: number): void {
    const parsed = parseAttributes(attributeSource);
    const ruleProblem = TAG_RULES[tag](parsed.attrs);
    const problem = (parsed.duplicate !== null) ? `repeats the attribute "${parsed.duplicate}"` : ruleProblem;

    if (problem !== null) {
      this.fail(`<${tag}> ${problem}`, this.index);
    }

    if (problem === null) {
      this.flushText();
      const element: TgElement = { kind: 'element', tag, attrs: parsed.attrs, children: [] };
      this.currentChildren().push(element);
      this.stack.push({ element, offset: this.index });
      this.index += tagLength;
    }
  }

  private readClosingTag(): void {
    const match = this.matchAt(CLOSE_TAG);
    const name = (match?.[1] ?? '').toLowerCase();
    const open = this.stack[this.stack.length - 1];

    if (match === null) {
      this.failUnmatchedLt();
    }

    if ((match !== null) && (open === undefined)) {
      this.fail(`</${name}> closes a tag that is not open`, this.index);
    }

    if ((match !== null) && (open !== undefined) && (open.element.tag !== name)) {
      this.fail(`</${name}> found where </${open.element.tag}> was expected`, this.index);
    }

    if ((match !== null) && (open !== undefined) && (open.element.tag === name)) {
      this.flushText();
      this.stack.pop();
      this.index += match[0].length;
    }
  }

  private currentChildren(): TgNode[] {
    const top = this.stack[this.stack.length - 1];
    const result = (top === undefined) ? this.root : top.element.children;

    return result;
  }

  private flushText(): void {
    if (this.text !== '') {
      this.currentChildren().push({ kind: 'text', text: this.text });
      this.length += this.text.length;
      this.text = '';
    }
  }

  /** A "<" that starts no valid tag: a broken tag when a name follows, else a bare "<". */
  private failUnmatchedLt(): void {
    TAG_START.lastIndex = this.index;
    const looksLikeTag = TAG_START.test(this.source);
    const message = looksLikeTag ? MALFORMED_TAG : BARE_LT;
    this.fail(message, this.index);
  }

  private fail(message: string, offset: number): void {
    const position = locate(this.source, offset);
    this.problem = { message, offset, line: position.line, column: position.column };
  }
}

/** Parses a post's content; the problem is the first one found, with its position. */
export function parseTelegramHtml(source: string): TgParseResult {
  const start = source.length - source.trimStart().length;
  const end = Math.max(start, source.trimEnd().length);
  const parser = new Parser(source, start, end);
  parser.run();

  return { nodes: parser.root, length: parser.length, problem: parser.problem };
}
