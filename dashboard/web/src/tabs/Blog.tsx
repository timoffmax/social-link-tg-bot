import { useCallback, useEffect, useRef, useState } from 'react';
import { getBlogPosts, type BlogPost, type BlogPostsResponse } from '../api';
import { PublishBadge } from '../components/Bits';
import { Panel } from '../components/Panel';
import { PostEditor } from '../components/PostEditor';
import { formatNumber, formatRelative, formatTimestamp } from '../format';
import { buildHash, useApi, type PostRef, type Query } from '../hooks';
import { BROADCAST_CONTENT_LIMIT } from '../telegramHtml';
import type { TabProps } from './types';

const NEW_POST: PostRef = 'new';

function postTitle(post: BlogPost): string {
  const result = (post.title.trim() === '') ? `Untitled post #${post.id}` : post.title;

  return result;
}

export function BlogTab({ days, tick, onLoaded, post, openPost, onDirtyChange }: TabProps) {
  const [version, setVersion] = useState(0);
  const query = useApi((signal) => getBlogPosts(signal).then(onLoaded), [tick, version]);
  // A post just created is handed to its editor, so opening it needs no fetch.
  const handoff = useRef<BlogPost | null>(null);
  const initial = (handoff.current?.id === post) ? handoff.current : null;
  const hrefFor = (target: PostRef) => buildHash({ tab: 'blog', days, post: target });

  useEffect(() => {
    if (handoff.current?.id !== post) {
      handoff.current = null;
    }
  }, [post]);

  const refreshList = useCallback(() => setVersion((value) => value + 1), []);

  const onSaved = useCallback((saved: BlogPost, created: boolean) => {
    refreshList();

    if (created) {
      handoff.current = saved;
      openPost(saved.id, true);
    }
  }, [openPost, refreshList]);

  const onDeleted = useCallback(() => {
    refreshList();
    openPost(null, true);
  }, [openPost, refreshList]);

  return (
    <div className={(post === null) ? 'blog' : 'blog blog--editing'}>
      <PostList query={query} selected={post} hrefFor={hrefFor} />
      {(post === null) && (
        <section className="panel editor editor--placeholder" aria-label="Editor">
          <p>Pick a post to edit it, or start a new one.</p>
          <a className="button button--primary" href={hrefFor(NEW_POST)}>New post</a>
        </section>
      )}
      {(post !== null) && (
        <PostEditor
          key={String(post)}
          postRef={post}
          initial={initial}
          timezone={query.data?.timezone ?? null}
          onSaved={onSaved}
          onChanged={refreshList}
          onDeleted={onDeleted}
          onClose={() => openPost(null)}
          onDirtyChange={onDirtyChange}
        />
      )}
    </div>
  );
}

interface PostListProps {
  query: Query<BlogPostsResponse>;
  selected: PostRef | null;
  hrefFor: (post: PostRef) => string;
}

function PostList({ query, selected, hrefFor }: PostListProps) {
  const data = query.data;
  const posts = data?.posts ?? [];

  return (
    <Panel
      title="Posts"
      subtitle="Newest first"
      loading={query.loading}
      error={query.error}
      hasData={(data !== null)}
      isEmpty={(posts.length === 0)}
      emptyText="No posts yet. Start one with New post; it stays a draft until you publish it."
      action={<a className="button button--primary button--small" href={hrefFor(NEW_POST)}>New post</a>}
    >
      {(data !== null) && (
        <ul className="post-list">
          {posts.map((item) => (
            <li key={item.id}>
              <PostRow post={item} data={data} isSelected={(item.id === selected)} href={hrefFor(item.id)} />
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

interface PostRowProps {
  post: BlogPost;
  data: BlogPostsResponse;
  isSelected: boolean;
  href: string;
}

function PostRow({ post, data, isSelected, href }: PostRowProps) {
  const isOver = (post.length > BROADCAST_CONTENT_LIMIT);

  return (
    <a className="post-row" href={href} aria-current={isSelected ? 'true' : undefined}>
      <span className="post-row__title">{postTitle(post)}</span>
      <span className="post-row__meta">
        <PublishBadge published={post.published} />
        <time dateTime={post.updated_at ?? undefined} title={`Updated ${formatTimestamp(post.updated_at, data.timezone)}`}>
          {formatRelative(post.updated_at, data.generated_at)}
        </time>
        <span className={isOver ? 'post-row__length is-over' : 'post-row__length'} title="Telegram length of the content, against the most the bot’s Broadcast preview can show">
          {formatNumber(post.length)} / {formatNumber(BROADCAST_CONTENT_LIMIT)}
        </span>
      </span>
    </a>
  );
}
