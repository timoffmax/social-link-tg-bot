// Unit tests for the preview parser: `npm test` (node --test with type stripping, no extra deps).
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { parseTelegramHtml, type TgNode } from '../src/telegramHtml.ts';

interface ValidCase {
  name: string;
  source: string;
  length: number;
}

interface InvalidCase {
  name: string;
  source: string;
  message: RegExp;
  line: number;
  column: number;
}

const VALID: ValidCase[] = [
  { name: 'plain text', source: 'Hello', length: 5 },
  { name: 'empty', source: '', length: 0 },
  { name: 'trims the content like the server', source: '  \n Hi \n ', length: 2 },
  { name: 'keeps inner whitespace and newlines', source: 'a\n\nb', length: 4 },
  { name: 'all simple tags', source: '<b>1</b><strong>2</strong><i>3</i><em>4</em><u>5</u><ins>6</ins><s>7</s><strike>8</strike><del>9</del>', length: 9 },
  { name: 'tags are not counted', source: '<b>bold <i>both</i></b>', length: 9 },
  { name: 'decodes the four entities', source: '&lt;b&gt; &amp; &quot;', length: 7 },
  { name: 'counts UTF-16 code units', source: '😀é', length: 3 },
  { name: 'spoiler span', source: '<span class="tg-spoiler">x</span>', length: 1 },
  { name: 'spoiler tag', source: '<tg-spoiler>x</tg-spoiler>', length: 1 },
  { name: 'link with href', source: '<a href="https://example.com/?a=1&amp;b=2">x</a>', length: 1 },
  { name: 'link with single-quoted href', source: "<a href='tg://user?id=1'>x</a>", length: 1 },
  { name: 'code with language', source: '<pre><code class="language-go">x := 1</code></pre>', length: 6 },
  { name: 'plain code', source: '<code>x</code>', length: 1 },
  { name: 'expandable blockquote', source: '<blockquote expandable>quote</blockquote>', length: 5 },
  { name: 'expandable with a value', source: '<blockquote expandable="yes">quote</blockquote>', length: 5 },
  { name: 'decimal and hex references', source: 'It&#39;s &#x1F600;&#X41;', length: 8 },
  { name: 'custom emoji', source: '<tg-emoji emoji-id="5368324170671202286">👍</tg-emoji>', length: 2 },
  { name: 'upper-case tag names', source: '<B>x</B>', length: 1 },
  { name: 'zero-padded reference', source: '&#0000000000000065;', length: 1 },
  { name: 'entity in the spoiler class', source: '<span class="tg&#45;spoiler">s</span>', length: 1 },
];

const INVALID: InvalidCase[] = [
  { name: 'unknown tag', source: 'Hi <div>x</div>', message: /<div> is not supported/, line: 1, column: 4 },
  { name: 'bare <', source: 'a < b', message: /bare "<"/, line: 1, column: 3 },
  { name: 'bare >', source: 'a > b', message: /bare ">"/, line: 1, column: 3 },
  { name: 'bare &', source: 'Tom & Jerry', message: /bare "&"/, line: 1, column: 5 },
  { name: 'zero numeric reference', source: '&#0;', message: /Invalid character reference &#0;/, line: 1, column: 1 },
  { name: 'surrogate numeric reference', source: 'a&#xD800;', message: /Invalid character reference/, line: 1, column: 2 },
  { name: 'out-of-range numeric reference', source: '&#x110000;', message: /Invalid character reference/, line: 1, column: 1 },
  { name: 'unclosed tag', source: 'x\n<b>bold', message: /<b> is never closed/, line: 2, column: 1 },
  { name: 'crossed nesting', source: '<b><i>x</b></i>', message: /<\/b> found where <\/i> was expected/, line: 1, column: 8 },
  { name: 'stray closing tag', source: 'x</b>', message: /closes a tag that is not open/, line: 1, column: 2 },
  { name: 'span without spoiler class', source: '<span>x</span>', message: /needs class="tg-spoiler"/, line: 1, column: 1 },
  { name: 'span with another class', source: '<span class="red">x</span>', message: /needs class="tg-spoiler"/, line: 1, column: 1 },
  { name: 'link with another attribute', source: '<a href="x" target="_blank">x</a>', message: /"target"/, line: 1, column: 1 },
  { name: 'bold with an attribute', source: '<b class="x">x</b>', message: /"class"/, line: 1, column: 1 },
  { name: 'code with a bad class', source: '<code class="go">x</code>', message: /language-/, line: 1, column: 1 },
  { name: 'emoji without id', source: '<tg-emoji>👍</tg-emoji>', message: /emoji-id/, line: 1, column: 1 },
  { name: 'repeated attribute', source: '<a href="a" href="b">x</a>', message: /repeats/, line: 1, column: 1 },
  { name: 'no-break space before an attribute', source: '<a\u00a0href="x">y</a>', message: /Malformed tag/, line: 1, column: 1 },
  { name: 'self-closing tag', source: 'x<br/>', message: /Malformed tag/, line: 1, column: 2 },
  { name: 'unterminated quote', source: '<a href="x>y</a>', message: /Malformed tag/, line: 1, column: 1 },
  { name: 'column counts characters after an emoji', source: '😀 <p>', message: /<p>/, line: 1, column: 3 },
];

describe('parseTelegramHtml', () => {
  for (const testCase of VALID) {
    it(`accepts ${testCase.name}`, () => {
      const result = parseTelegramHtml(testCase.source);

      assert.equal(result.problem, null);
      assert.equal(result.length, testCase.length);
    });
  }

  for (const testCase of INVALID) {
    it(`rejects ${testCase.name}`, () => {
      const problem = parseTelegramHtml(testCase.source).problem;

      assert.notEqual(problem, null);
      assert.match(problem?.message ?? '', testCase.message);
      assert.equal(problem?.line, testCase.line);
      assert.equal(problem?.column, testCase.column);
    });
  }

  it('reports offsets into the untrimmed content', () => {
    const problem = parseTelegramHtml('\n\n  a & b').problem;

    assert.equal(problem?.offset, 6);
    assert.equal(problem?.line, 3);
    assert.equal(problem?.column, 5);
  });

  it('builds a nested tree with decoded text and attributes', () => {
    const expected: TgNode[] = [
      { kind: 'text', text: 'Hi ' },
      {
        kind: 'element',
        tag: 'a',
        attrs: { href: 'https://x.test/?a=1&b=2' },
        children: [
          { kind: 'element', tag: 'b', attrs: {}, children: [{ kind: 'text', text: '<you>' }] },
        ],
      },
    ];

    assert.deepEqual(parseTelegramHtml('Hi <a href="https://x.test/?a=1&amp;b=2"><b>&lt;you&gt;</b></a>').nodes, expected);
  });

  it('marks bare attributes', () => {
    const nodes = parseTelegramHtml('<blockquote expandable>x</blockquote>').nodes;

    assert.deepEqual(nodes[0], { kind: 'element', tag: 'blockquote', attrs: { expandable: '' }, children: [{ kind: 'text', text: 'x' }] });
  });
});
