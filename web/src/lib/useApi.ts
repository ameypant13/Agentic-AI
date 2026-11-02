import { useCallback, useEffect, useRef, useState } from "react";

export type ApiState<T> = {
  data: T | undefined;
  error: Error | undefined;
  loading: boolean;
  reload: () => void;
  setData: (update: T | ((prev: T | undefined) => T | undefined)) => void;
};

/** Loads `fn()` on mount and whenever `deps` change. Ignores results of stale calls. */
export function useApi<T>(fn: () => Promise<T>, deps: unknown[] = []): ApiState<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<Error | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const callId = useRef(0);

  useEffect(() => {
    const id = ++callId.current;
    setLoading(true);
    setError(undefined);
    fn().then(
      (d) => {
        if (id !== callId.current) return;
        setData(d);
        setLoading(false);
      },
      (e: unknown) => {
        if (id !== callId.current) return;
        setError(e instanceof Error ? e : new Error(String(e)));
        setLoading(false);
      },
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload, setData: setData as ApiState<T>["setData"] };
}
