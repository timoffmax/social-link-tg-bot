import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { errorField, FORMAT_TOOLS, safeHref, titleLength, wrapSelection, type FormatTool } from '../src/editing.ts';

function tool(id: string): FormatTool {
  const result = FORMAT_TOOLS.find((candidate) => (candidate.id === id));
  assert.ok(result);

  return result;
}

describe('wrapSelection', () => {
  it('wraps the selection and keeps it selected inside the tags', () => {
    const wrapped = wrapSelection('say hello now', 4, 9, tool('bold'));

    assert.equal(wrapped.insert, '<b>hello</b>');
    assert.equal(wrapped.selectionStart, 7);
    assert.equal(wrapped.selectionEnd, 12);
  });

  it('puts the caret between the tags when nothing is selected', () => {
    const wrapped = wrapSelection('ab', 1, 1, tool('spoiler'));

    assert.equal(wrapped.insert, '<tg-spoiler></tg-spoiler>');
    assert.equal(wrapped.selectionStart, 13);
    assert.equal(wrapped.selectionEnd, 13);
  });

  it('puts the caret into the empty href of a link', () => {
    const wrapped = wrapSelection('x site', 2, 6, tool('link'));

    assert.equal(wrapped.insert, '<a href="">site</a>');
    assert.equal(wrapped.selectionStart, 11);
    assert.equal(wrapped.selectionEnd, 11);
  });

  it('offers exactly the tags the addendum lists', () => {
    assert.deepEqual(FORMAT_TOOLS.map((item) => item.open), ['<b>', '<i>', '<u>', '<s>', '<code>', '<a href="">', '<tg-spoiler>', '<blockquote>']);
  });
});

describe('titleLength', () => {
  it('counts trimmed code points', () => {
    assert.equal(titleLength('  😀ab  '), 3);
  });
});

describe('safeHref', () => {
  it('allows web, tg and mail links', () => {
    assert.equal(safeHref('https://t.me/x'), 'https://t.me/x');
    assert.equal(safeHref('tg://user?id=1'), 'tg://user?id=1');
    assert.equal(safeHref('mailto:a@b.test'), 'mailto:a@b.test');
  });

  it('refuses scripts, relative and empty links', () => {
    assert.equal(safeHref('javascript:alert(1)'), null);
    assert.equal(safeHref('JavaScript:alert(1)'), null);
    assert.equal(safeHref('/relative'), null);
    assert.equal(safeHref(''), null);
  });
});

describe('errorField', () => {
  it('maps title messages to the title field and everything else to the content', () => {
    assert.equal(errorField('title is required'), 'title');
    assert.equal(errorField('Title must be 1 to 255 characters'), 'title');
    assert.equal(errorField('content: line 1, column 3: bad tag'), 'content');
    assert.equal(errorField('<a> has an attribute "title" Telegram does not allow'), 'content');
  });
});
