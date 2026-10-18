import {
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
  type RefObject,
} from 'react';
import {
  ApiError,
  createBlogPost,
  deleteBlogPost,
  getBlogPost,
  HTTP_CONFLICT,
  HTTP_NOT_FOUND,
  setBlogPostPublished,
  updateBlogPost,
  type BlogPost,
  type BlogPostResponse,
} from '../api';
import {
  errorField,
  FORMAT_TOOLS,
  isSameDraft,
  TITLE_MAX_CHARS,
  titleLength,
  wrapSelection,
  type Draft,
  type DraftField,
  type FormatTool,
} from '../editing';
import { formatClock, formatNumber, formatTimestamp } from '../format';
import type { PostRef } from '../hooks';
import {
  BROADCAST_CONTENT_LIMIT,
  BROADCAST_FRAME_LENGTH,
  parseTelegramHtml,
  TELEGRAM_TEXT_LIMIT,
  type TgNode,
  type TgParseResult,
} from '../telegramHtml';
import { PublishBadge } from './Bits';
import { TelegramPreview } from './TelegramPreview';

type Busy = 'save' | 'publish' | 'delete' | 'reload';
type LoadState = 'loading' | 'ready' | 'missing' | 'failed';

interface FieldError {
  field: DraftField;
  message: string;
}

const EMPTY_DRAFT: Draft = { title: '', content: '' };
const HTTP_BAD_REQUEST = 400;
const NEW_POST: PostRef = 'new';
const TOOL_SHORTCUTS: Record<string, string> = { b: 'bold', i: 'italic', u: 'underline' };
const SAVE_SHORTCUT = 's';
const COUNTER_HINT = `Characters Telegram counts (the text without tags, entities decoded). The limit is below Telegram’s ${TELEGRAM_TEXT_LIMIT} because the bot’s Broadcast preview adds up to ${BROADCAST_FRAME_LENGTH} around the post.`;
const GONE_MESSAGE = 'This post no longer exists: it was deleted elsewhere. Copy your text if you still need it.';

function draftOf(post: BlogPost): Draft {
  return { title: post.title, content: post.content };
}

function statusOf(error: unknown): number {
  const result = (error instanceof ApiError) ? error.status : 0;

  return result;
}

function messageOf(error: unknown): string {
  const result = (error instanceof Error) ? error.message : 'Unknown error';

  return result;
}

function isAbort(error: unknown): boolean {
  return (error instanceof DOMException) && (error.name === 'AbortError');
}

/** A delete of a post that is already gone counts as done. */
function ignoreNotFound(error: unknown): void {
  if (statusOf(error) !== HTTP_NOT_FOUND) {
    throw error;
  }
}

function localTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone;
}

export interface PostEditorProps {
  postRef: PostRef;
  /** A post the tab just created, so the editor needs no fetch. */
  initial: BlogPost | null;
  timezone: string | null;
  onSaved: (post: BlogPost, created: boolean) => void;
  /** Something the list shows changed (published flag). */
  onChanged: () => void;
  onDeleted: () => void;
  onClose: () => void;
  onDirtyChange: (dirty: boolean) => void;
}

interface EditorCallbacks {
  onSaved: PostEditorProps['onSaved'];
  onChanged: PostEditorProps['onChanged'];
  onDeleted: PostEditorProps['onDeleted'];
}

/** Loading, the draft and the four writes, with their errors. */
function usePostEditor(postRef: PostRef, initial: BlogPost | null, callbacks: EditorCallbacks) {
  const needsLoad = (postRef !== NEW_POST) && (initial === null);
  const [base, setBase] = useState<BlogPost | null>(initial);
  const [draft, setDraft] = useState<Draft>((initial === null) ? EMPTY_DRAFT : draftOf(initial));
  const [loadState, setLoadState] = useState<LoadState>(needsLoad ? 'loading' : 'ready');
  const [loadError, setLoadError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Busy | null>(null);
  const [fieldError, setFieldError] = useState<FieldError | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  // An initial post is only handed over right after it was created.
  const [notice, setNotice] = useState<string | null>((initial === null) ? null : 'Post created.');
  const [attempt, setAttempt] = useState(0);
  // The latest draft for code that runs after an await, where the rendered draft can be stale.
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const savedDraft = (base === null) ? EMPTY_DRAFT : draftOf(base);
  const dirty = (isSameDraft(draft, savedDraft) === false);
  // A created post reopens in a fresh editor, so text typed meanwhile would be lost: the fields wait.
  const isCreating = (busy === 'save') && (base === null);

  const applyLoaded = (post: BlogPost) => {
    setBase(post);
    setDraft(draftOf(post));
    setLoadState('ready');
  };

  useEffect(() => {
    const controller = new AbortController();

    if (needsLoad && (typeof postRef === 'number')) {
      getBlogPost(postRef, controller.signal)
        .then((response) => applyLoaded(response.post))
        .catch((error: unknown) => {
          const isMissing = (statusOf(error) === HTTP_NOT_FOUND);

          if (isAbort(error) === false) {
            setLoadState(isMissing ? 'missing' : 'failed');
            setLoadError(messageOf(error));
          }
        });
    }

    return () => controller.abort();
    // Keyed by the post: loads once per mount and on retry.
  }, [attempt]);

  const retryLoad = () => {
    setLoadState('loading');
    setAttempt((value) => value + 1);
  };

  const handleError = (error: unknown) => {
    const message = messageOf(error);
    const handlers: Record<number, () => void> = {
      [HTTP_CONFLICT]: () => setConflict(true),
      [HTTP_BAD_REQUEST]: () => setFieldError({ field: errorField(message), message }),
      [HTTP_NOT_FOUND]: () => setFormError(GONE_MESSAGE),
    };
    const status = statusOf(error);
    const handler = handlers[status] ?? (() => setFormError(`The server didn’t accept the change (${message}). Try again.`));
    handler();
  };

  const run = async (kind: Busy, action: () => Promise<void>) => {
    if (busy === null) {
      setBusy(kind);
      setFormError(null);
      setNotice(null);

      try {
        await action();
      } catch (error) {
        handleError(error);
      } finally {
        setBusy(null);
      }
    }
  };

  /** Creates the post, or updates it with the updated_at it was loaded with. */
  const persist = (sent: Draft): Promise<BlogPostResponse> => {
    const result = (base === null) ? createBlogPost(sent) : updateBlogPost(base.id, { ...sent, updated_at: base.updated_at });

    return result;
  };

  const save = () => run('save', async () => {
    const sent = draft;
    const created = (base === null);
    setFieldError(null);
    const response = await persist(sent);
    const post = response.post;
    setBase(post);
    // Text typed while the request ran is kept; otherwise show what the server stored (trimmed).
    setDraft((current) => (isSameDraft(current, sent) ? draftOf(post) : current));
    setConflict(false);
    setNotice(created ? 'Post created.' : 'Changes saved.');
    callbacks.onSaved(post, created);
  });

  const togglePublished = () => run('publish', async () => {
    if (base !== null) {
      const loaded = draftOf(base);
      const response = await setBlogPostPublished(base.id, (base.published === false));
      const post = response.post;
      const isUnchanged = isSameDraft(draftOf(post), loaded);
      // Read after the await: text typed while the request ran counts as a local edit.
      const isDirtyNow = (isSameDraft(draftRef.current, loaded) === false);
      // Edited elsewhere while there are local edits: keep the old updated_at so saving reports the conflict.
      const adopt = isUnchanged || (isDirtyNow === false);
      setBase(adopt ? post : { ...base, published: post.published });

      if (isUnchanged === false) {
        // Only an untouched draft takes the text edited elsewhere; a newer local edit is never replaced.
        setDraft((current) => (isSameDraft(current, loaded) ? draftOf(post) : current));
      }

      setNotice(post.published ? 'Published.' : 'Unpublished.');
      callbacks.onChanged();
    }
  });

  const remove = () => run('delete', async () => {
    if (base !== null) {
      await deleteBlogPost(base.id).catch(ignoreNotFound);
      callbacks.onDeleted();
    }
  });

  const reload = () => run('reload', async () => {
    if (base !== null) {
      const response = await getBlogPost(base.id);
      applyLoaded(response.post);
      setConflict(false);
      setFieldError(null);
      setNotice('Loaded the latest version.');
    }
  });

  const edit = (next: Draft) => {
    draftRef.current = next;
    setDraft(next);
    setFieldError(null);
    setNotice(null);
  };

  return {
    base,
    draft,
    dirty,
    isCreating,
    loadState,
    loadError,
    busy,
    fieldError,
    formError,
    conflict,
    notice,
    edit,
    save,
    togglePublished,
    remove,
    reload,
    retryLoad,
    dismissConflict: () => setConflict(false),
  };
}

type Editor = ReturnType<typeof usePostEditor>;

/** Keeps the browser's own leave warning and the tab guard in step with unsaved edits. */
function useLeaveWarning(dirty: boolean, onDirtyChange: (dirty: boolean) => void): void {
  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);

  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);

  useEffect(() => {
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };

    if (dirty) {
      window.addEventListener('beforeunload', onBeforeUnload);
    }

    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirty]);
}

export function PostEditor(props: PostEditorProps) {
  const { postRef, initial, timezone, onClose, onDirtyChange } = props;
  const editor = usePostEditor(postRef, initial, props);
  const headingId = useId();
  useLeaveWarning(editor.dirty, onDirtyChange);

  return (
    <section className="panel editor" aria-labelledby={headingId}>
      <button type="button" className="link-button editor__back" onClick={onClose}>‹ All posts</button>
      {(editor.loadState === 'loading') && (
        <>
          <h2 id={headingId} className="panel__title">Post #{postRef}</h2>
          <div className="state state--loading" role="status"><span className="spinner" aria-hidden="true" />Loading…</div>
        </>
      )}
      {(editor.loadState === 'missing') && (
        <>
          <h2 id={headingId} className="panel__title">Post #{postRef}</h2>
          <div className="state state--empty">This post no longer exists. It may have been deleted in the bot’s owner panel.</div>
        </>
      )}
      {(editor.loadState === 'failed') && (
        <>
          <h2 id={headingId} className="panel__title">Post #{postRef}</h2>
          <div className="state state--error" role="alert">
            <strong>Couldn’t load this post</strong> ({editor.loadError}).{' '}
            <button type="button" className="link-button" onClick={editor.retryLoad}>Try again</button>
          </div>
        </>
      )}
      {(editor.loadState === 'ready') && <EditorForm editor={editor} headingId={headingId} timezone={timezone} />}
    </section>
  );
}

interface EditorFormProps {
  editor: Editor;
  headingId: string;
  timezone: string | null;
}

function EditorForm({ editor, headingId, timezone }: EditorFormProps) {
  const { base, draft, busy, fieldError } = editor;
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const parsed = useMemo(() => parseTelegramHtml(draft.content), [draft.content]);
  const lastGood = useRef<TgNode[]>(parsed.nodes);
  const titleId = useId();
  const contentId = useId();
  const zone = timezone ?? localTimezone();

  if (parsed.problem === null) {
    lastGood.current = parsed.nodes;
  }

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    void editor.save();
  };

  const onKeyDown = (event: KeyboardEvent) => {
    const isSave = (event.ctrlKey || event.metaKey) && (event.key.toLowerCase() === SAVE_SHORTCUT);

    if (isSave) {
      event.preventDefault();
      void editor.save();
    }
  };

  return (
    <form className="editor__form" onSubmit={onSubmit} onKeyDown={onKeyDown} noValidate>
      <header className="editor__head">
        <h2 id={headingId} className="panel__title">{(base === null) ? 'New post' : `Post #${base.id}`}</h2>
        {(base !== null) && <PublishBadge published={base.published} />}
        {(base !== null) && (
          <p className="panel__subtitle editor__dates">
            <span>Created {formatTimestamp(base.created_at, zone)}</span>
            <span>Updated {formatTimestamp(base.updated_at, zone)}</span>
          </p>
        )}
      </header>

      {editor.conflict && <ConflictNotice editor={editor} />}

      <TitleField
        id={titleId}
        editor={editor}
        error={(fieldError?.field === 'title') ? fieldError.message : null}
      />

      <div className="editor__columns">
        <ContentField
          id={contentId}
          editor={editor}
          parsed={parsed}
          textarea={textarea}
          error={(fieldError?.field === 'content') ? fieldError.message : null}
        />
        <div className="field">
          <div className="field__label-row">
            <span className="field__label">Preview</span>
            <span className="field__hint">As the message appears in Telegram</span>
          </div>
          <TelegramPreview
            nodes={(parsed.problem === null) ? parsed.nodes : lastGood.current}
            stale={(parsed.problem !== null)}
            time={formatClock(new Date().toISOString(), zone)}
          />
          {(parsed.problem !== null) && <p className="field__hint">Showing the last version without problems.</p>}
        </div>
      </div>

      {(editor.formError !== null) && <div className="state state--error" role="alert">{editor.formError}</div>}

      <div className="editor__actions">
        <button type="submit" className="button button--primary" disabled={(editor.dirty === false) || (busy !== null)}>
          {(busy === 'save') ? 'Saving…' : ((base === null) ? 'Create post' : 'Save changes')}
        </button>
        {(base !== null) && (
          <button type="button" className="button" onClick={() => void editor.togglePublished()} disabled={(busy !== null)}>
            {(busy === 'publish') ? 'Updating…' : (base.published ? 'Unpublish' : 'Publish')}
          </button>
        )}
        {(base !== null) && (
          <button
            type="button"
            className="button button--danger-quiet"
            onClick={() => setConfirmingDelete(true)}
            disabled={(busy !== null) || confirmingDelete}
          >
            Delete
          </button>
        )}
        <span className="editor__status" role="status">
          {editor.dirty ? 'Unsaved changes' : (editor.notice ?? '')}
        </span>
      </div>

      {confirmingDelete && (base !== null) && (
        <DeleteConfirm
          title={base.title}
          busy={(busy === 'delete')}
          onConfirm={() => void editor.remove()}
          onCancel={() => setConfirmingDelete(false)}
        />
      )}

      <p className="editor__note">
        Telegram receives only the content: the broadcast sends it as the message text, without the title.
        Published posts are listed in the bot under /owner → 📢 Broadcast, where you pick the audience and send; publishing here sends nothing.
      </p>
    </form>
  );
}

function ConflictNotice({ editor }: { editor: Editor }) {
  return (
    <div className="notice notice--warning" role="alert">
      <p>
        <strong>This post was changed elsewhere</strong>, probably in the bot’s owner panel, after you opened it.
        Saving now would overwrite that change, so it was not saved. Load the latest version to continue
        (your edits here are replaced: copy them first if you need them).
      </p>
      <div className="notice__actions">
        <button type="button" className="button button--primary" onClick={() => void editor.reload()} disabled={(editor.busy !== null)}>
          {(editor.busy === 'reload') ? 'Loading…' : 'Load the latest version'}
        </button>
        <button type="button" className="button button--quiet" onClick={editor.dismissConflict}>Keep my edits for now</button>
      </div>
    </div>
  );
}

interface TitleFieldProps {
  id: string;
  editor: Editor;
  error: string | null;
}

function TitleField({ id, editor, error }: TitleFieldProps) {
  const length = titleLength(editor.draft.title);
  const isOver = (length > TITLE_MAX_CHARS);
  const errorId = `${id}-error`;

  return (
    <div className="field">
      <div className="field__label-row">
        <label className="field__label" htmlFor={id}>Title</label>
        <span className={isOver ? 'field__count is-over' : 'field__count'}>
          {formatNumber(length)} / {formatNumber(TITLE_MAX_CHARS)}
        </span>
      </div>
      <input
        id={id}
        className="input"
        type="text"
        value={editor.draft.title}
        onChange={(event) => editor.edit({ ...editor.draft, title: event.target.value })}
        readOnly={editor.isCreating}
        aria-invalid={(error !== null) || isOver}
        aria-describedby={(error === null) ? undefined : errorId}
        autoComplete="off"
      />
      <p className="field__hint">For finding the post here and in the owner panel; it is not sent.</p>
      {(error !== null) && <p id={errorId} className="field__error" role="alert">{error}</p>}
    </div>
  );
}

interface ContentFieldProps {
  id: string;
  editor: Editor;
  parsed: TgParseResult;
  textarea: RefObject<HTMLTextAreaElement>;
  error: string | null;
}

function ContentField({ id, editor, parsed, textarea, error }: ContentFieldProps) {
  const pendingSelection = useRef<[number, number] | null>(null);
  const problem = parsed.problem;
  const isOver = (problem === null) && (parsed.length > BROADCAST_CONTENT_LIMIT);
  const describedBy = `${id}-problems`;

  useLayoutEffect(() => {
    const element = textarea.current;
    const selection = pendingSelection.current;

    if ((element !== null) && (selection !== null)) {
      element.setSelectionRange(selection[0], selection[1]);
      pendingSelection.current = null;
    }
  }, [editor.draft.content, textarea]);

  const applyTool = (tool: FormatTool) => {
    const element = textarea.current;

    if ((element !== null) && (editor.isCreating === false)) {
      const { selectionStart, selectionEnd, value } = element;
      const wrapped = wrapSelection(value, selectionStart, selectionEnd, tool);
      pendingSelection.current = [wrapped.selectionStart, wrapped.selectionEnd];
      element.focus();
      // insertText keeps the browser's undo history; the fallback sets the value directly.
      const inserted = document.execCommand('insertText', false, wrapped.insert);

      if (inserted === false) {
        const next = `${value.slice(0, selectionStart)}${wrapped.insert}${value.slice(selectionEnd)}`;
        editor.edit({ ...editor.draft, content: next });
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

  const showProblem = () => {
    const element = textarea.current;

    if ((element !== null) && (problem !== null)) {
      element.setSelectionRange(problem.offset, problem.offset + 1);
      element.blur();
      element.focus();
    }
  };

  return (
    <div className="field">
      <div className="field__label-row">
        <label className="field__label" htmlFor={id}>Content</label>
        <LengthCounter parsed={parsed} />
      </div>
      <Toolbar controls={id} disabled={editor.isCreating} onApply={applyTool} />
      <textarea
        id={id}
        ref={textarea}
        className="input input--code"
        rows={14}
        value={editor.draft.content}
        onChange={(event) => editor.edit({ ...editor.draft, content: event.target.value })}
        onKeyDown={onKeyDown}
        readOnly={editor.isCreating}
        aria-invalid={(problem !== null) || isOver || (error !== null)}
        aria-describedby={describedBy}
        spellCheck
      />
      <div id={describedBy}>
        {(problem !== null) && (
          <p className="field__error" role="alert">
            Telegram would reject this. Line {problem.line}, column {problem.column}: {problem.message}.{' '}
            <button type="button" className="link-button" onClick={showProblem}>Show in the text</button>
          </p>
        )}
        {isOver && (
          <p className="field__error">
            Posts can have {formatNumber(BROADCAST_CONTENT_LIMIT)} characters; this post is {formatNumber(parsed.length - BROADCAST_CONTENT_LIMIT)} over.
            Telegram allows {formatNumber(TELEGRAM_TEXT_LIMIT)}, and the bot’s Broadcast preview adds up to {formatNumber(BROADCAST_FRAME_LENGTH)} of its own around the post.
          </p>
        )}
        {(error !== null) && <p className="field__error" role="alert">Not saved: {error}</p>}
      </div>
    </div>
  );
}

function LengthCounter({ parsed }: { parsed: TgParseResult }) {
  const isCounted = (parsed.problem === null);
  const isOver = isCounted && (parsed.length > BROADCAST_CONTENT_LIMIT);
  const shown = isCounted ? formatNumber(parsed.length) : '–';

  return (
    <span
      className={isOver ? 'field__count is-over' : 'field__count'}
      title={COUNTER_HINT}
    >
      {shown} / {formatNumber(BROADCAST_CONTENT_LIMIT)}
    </span>
  );
}

interface ToolbarProps {
  controls: string;
  disabled: boolean;
  onApply: (tool: FormatTool) => void;
}

function Toolbar({ controls, disabled, onApply }: ToolbarProps) {
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

interface DeleteConfirmProps {
  title: string;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

function DeleteConfirm({ title, busy, onConfirm, onCancel }: DeleteConfirmProps) {
  const cancelButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    cancelButton.current?.focus();
  }, []);

  return (
    <div className="notice notice--danger" role="alertdialog" aria-label="Confirm delete">
      <p>
        Delete “{title}” for good? It also disappears from the bot’s owner panel. This can’t be undone.
      </p>
      <div className="notice__actions">
        <button type="button" className="button button--danger" onClick={onConfirm} disabled={busy}>
          {busy ? 'Deleting…' : 'Delete post'}
        </button>
        <button ref={cancelButton} type="button" className="button button--quiet" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </div>
  );
}
