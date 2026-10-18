import { useLayoutEffect, useRef, type KeyboardEvent, type RefObject } from 'react';
import { FORMAT_TOOLS, wrapSelection, type FormatTool } from '../editing';
import { formatNumber } from '../format';
import type { TgParseResult } from '../telegramHtml';

// The Telegram HTML editing pieces the blog editor and the report reply
// composer share: the formatting toolbar with its shortcuts and the length counter.

const TOOL_SHORTCUTS: Record<string, string> = { b: 'bold', i: 'italic', u: 'underline' };

export interface FormatTools {
  applyTool: (tool: FormatTool) => void;
  /** Ctrl/Cmd+B, I and U on the textarea. */
  onKeyDown: (event: KeyboardEvent<HTMLTextAreaElement>) => void;
}

/**
 * Wraps the textarea's selection in a tool's tags and restores the selection after React
 * re-renders; onChange receives the new value when the browser cannot insert the text itself.
 */
export function useFormatTools(
  textarea: RefObject<HTMLTextAreaElement>,
  value: string,
  onChange: (next: string) => void,
  disabled: boolean,
): FormatTools {
  const pendingSelection = useRef<[number, number] | null>(null);

  useLayoutEffect(() => {
    const element = textarea.current;
    const selection = pendingSelection.current;

    if ((element !== null) && (selection !== null)) {
      element.setSelectionRange(selection[0], selection[1]);
      pendingSelection.current = null;
    }
  }, [value, textarea]);

  const applyTool = (tool: FormatTool) => {
    const element = textarea.current;

    if ((element !== null) && (disabled === false)) {
      const { selectionStart, selectionEnd } = element;
      const current = element.value;
      const wrapped = wrapSelection(current, selectionStart, selectionEnd, tool);
      pendingSelection.current = [wrapped.selectionStart, wrapped.selectionEnd];
      element.focus();
      // insertText keeps the browser's undo history; the fallback sets the value directly.
      const inserted = document.execCommand('insertText', false, wrapped.insert);

      if (inserted === false) {
        const next = `${current.slice(0, selectionStart)}${wrapped.insert}${current.slice(selectionEnd)}`;
        onChange(next);
      }
    }
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    const toolId = TOOL_SHORTCUTS[event.key.toLowerCase()];
    const tool = FORMAT_TOOLS.find((candidate) => (candidate.id === toolId));
    const isShortcut = (event.ctrlKey || event.metaKey) && (event.altKey === false) && (tool !== undefined);

    if (isShortcut && (tool !== undefined)) {
      event.preventDefault();
      applyTool(tool);
    }
  };

  return { applyTool, onKeyDown };
}

interface LengthCounterProps {
  parsed: TgParseResult;
  limit: number;
  /** Tooltip: what is counted and where the limit comes from. */
  hint: string;
}

/** Characters as Telegram counts them against the limit; a dash while the text has a problem. */
export function LengthCounter({ parsed, limit, hint }: LengthCounterProps) {
  const isCounted = (parsed.problem === null);
  const isOver = isCounted && (parsed.length > limit);
  const shown = isCounted ? formatNumber(parsed.length) : '–';

  return (
    <span className={isOver ? 'field__count is-over' : 'field__count'} title={hint}>
      {shown} / {formatNumber(limit)}
    </span>
  );
}

interface ToolbarProps {
  controls: string;
  disabled: boolean;
  onApply: (tool: FormatTool) => void;
}

export function Toolbar({ controls, disabled, onApply }: ToolbarProps) {
  return (
    <div className="toolbar" role="toolbar" aria-label="Formatting" aria-controls={controls}>
      {FORMAT_TOOLS.map((tool) => (
        <button
          key={tool.id}
          type="button"
          className={`toolbar__button toolbar__button--${tool.id}`}
          title={`${tool.title}: ${tool.open}…${tool.close}`}
          aria-label={tool.title}
          // Keep the textarea's selection: a mouse press would otherwise move focus first.
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => onApply(tool)}
          disabled={disabled}
        >
          {tool.label}
        </button>
      ))}
    </div>
  );
}
