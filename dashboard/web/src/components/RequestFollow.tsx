import { useCallback, useEffect, useRef, useState } from 'react';
import { getSettingRequest, type SettingRequest } from '../api';
import { APPLIED_SHOWN_MS, pollDelay, requestStatusText, serverNow, shouldPollRequest } from '../settings';

// Following a queued change (a setting or a limit) until the bot decides it, shared by the
// Settings and Limits tabs.

/**
 * Follows one queued change until the bot decides it or it expires: every second for 15 s, then
 * every 5 s. A loop goes on only while the row is mounted and no newer loop has replaced it.
 */
export function useRequestFollow(onFinal: () => void, offset: number) {
  const [request, setRequest] = useState<SettingRequest | null>(null);
  const loop = useRef<{ timer: number | undefined; controller: AbortController } | null>(null);
  const finalRef = useRef(onFinal);
  const offsetRef = useRef(offset);
  finalRef.current = onFinal;
  offsetRef.current = offset;

  const stop = useCallback(() => {
    const current = loop.current;

    if (current !== null) {
      window.clearTimeout(current.timer);
      current.controller.abort();
    }

    loop.current = null;
  }, []);

  const follow = useCallback((started: SettingRequest) => {
    stop();
    const startedAt = Date.now();
    const own = { timer: undefined as number | undefined, controller: new AbortController() };
    loop.current = own;
    setRequest(started);
    const isCurrent = () => (loop.current === own);
    // The last answer: a waiting approval has a later expiry than the change had when queued.
    let latest = started;
    const schedule = (latest: SettingRequest) => {
      const now = serverNow(offsetRef.current);
      const isWaiting = shouldPollRequest(latest.status, latest.expires_at, now);

      if (isCurrent() && isWaiting) {
        own.timer = window.setTimeout(poll, pollDelay(Date.now() - startedAt));
      }

      if (isCurrent() && (isWaiting === false)) {
        loop.current = null;
        finalRef.current();
      }
    };
    const poll = () => {
      getSettingRequest(started.id, own.controller.signal)
        .then((response) => {
          if (isCurrent()) {
            latest = response.request;
            setRequest(latest);
            schedule(latest);
          }
        })
        .catch(() => {
          // A failed check (network, restart) is retried until the change expires.
          if (isCurrent()) {
            schedule(latest);
          }
        });
    };
    schedule(started);
  }, [stop]);

  const clear = useCallback(() => {
    stop();
    setRequest(null);
  }, [stop]);

  useEffect(() => stop, [stop]);

  return { request, follow, clear };
}

/** Where a queued change stands; an applied one fades after a few seconds. */
export function RequestStatus({ request, offset }: { request: SettingRequest; offset: number }) {
  const [isFaded, setFaded] = useState(false);
  const now = serverNow(offset);
  const isQueued = (request.status === 'queued') && shouldPollRequest(request.status, request.expires_at, now);
  const isApplied = (request.status === 'applied');
  const text = requestStatusText(request, now);

  useEffect(() => {
    const timer = (request.status === 'applied') ? window.setTimeout(() => setFaded(true), APPLIED_SHOWN_MS) : undefined;

    return () => window.clearTimeout(timer);
  }, [request.status]);

  return (
    <div className={`setting__status setting__status--${request.status}${isFaded ? ' is-faded' : ''}`} role="status">
      {isQueued && <span className="spinner" aria-hidden="true" />}
      {isApplied && <><span aria-hidden="true">✓</span> </>}
      {text}
    </div>
  );
}
