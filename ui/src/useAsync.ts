import { useEffect, useState } from "react";

export type Async<T> = { loading: true } | { loading: false; data: T } | { loading: false; error: Error };

// useAsync runs load whenever deps change and ignores stale results.
export function useAsync<T>(load: () => Promise<T>, deps: unknown[]): Async<T> {
  const [state, setState] = useState<Async<T>>({ loading: true });
  useEffect(() => {
    let live = true;
    setState({ loading: true });
    load().then(
      (data) => live && setState({ loading: false, data }),
      (error: Error) => live && setState({ loading: false, error }),
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return state;
}
