import { useContext, useEffect, useRef, useState } from "react";
import { Generation } from "./status";

export type Async<T> =
  | { loading: true }
  | { loading: false; data: T }
  | { loading: false; error: Error };

// useAsync runs load whenever deps or the analysis generation change and
// ignores stale results. A reload for a new generation keeps showing the
// old data until the new data arrives.
export function useAsync<T>(load: () => Promise<T>, deps: unknown[]): Async<T> {
  const [state, setState] = useState<Async<T>>({ loading: true });
  const gen = useContext(Generation);
  const last = useRef<unknown[]>(undefined);
  useEffect(() => {
    let live = true;
    const refresh =
      last.current !== undefined &&
      last.current.length === deps.length &&
      last.current.every((d, i) => Object.is(d, deps[i]));
    last.current = deps;
    if (!refresh) setState({ loading: true });
    load().then(
      (data) => live && setState({ loading: false, data }),
      (error: Error) => live && setState({ loading: false, error }),
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, gen]);
  return state;
}
