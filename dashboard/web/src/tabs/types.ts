import type { RangeDays, ResponseMeta } from '../api';
import type { PostRef } from '../hooks';

export interface TabProps {
  days: RangeDays;
  /** Bumps on auto-refresh and on the refresh button. */
  tick: number;
  /** Reports each response so the header can show "updated hh:mm". */
  onLoaded: <T extends ResponseMeta>(response: T) => T;
  /** The post open in the Blog tab; null elsewhere. */
  post: PostRef | null;
  /** Opens a post in the Blog tab (null: the list); force skips the unsaved-changes guard. */
  openPost: (post: PostRef | null, force?: boolean) => void;
  /** The Blog editor reports unsaved changes so leaving can be guarded. */
  onDirtyChange: (dirty: boolean) => void;
}
