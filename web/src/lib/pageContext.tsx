import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

/**
 * What the user is looking at, for the Copilot dock and the command palette.
 * Pages call usePageContext({...}) with their subject; the dock shows it as a
 * context pill and prefixes it to questions.
 */
export type PageContextValue = {
  /** Short label, e.g. "Tenant acme-prod". */
  label: string;
  /** Text sent to the Copilot ahead of the question, e.g. tenant, cluster. */
  prompt: string;
  /** Suggested questions for this page. */
  suggestions?: string[];
};

type Ctx = { value: PageContextValue | null; set: (v: PageContextValue | null) => void };

const PageCtx = createContext<Ctx>({ value: null, set: () => {} });

export function PageContextProvider({ children }: { children: ReactNode }) {
  const [value, set] = useState<PageContextValue | null>(null);
  return <PageCtx.Provider value={{ value, set }}>{children}</PageCtx.Provider>;
}

/** Read the current page's context (the dock and palette use this). */
export function useCurrentPageContext(): PageContextValue | null {
  return useContext(PageCtx).value;
}

/** Declare this page's context; cleared when the page unmounts. */
export function usePageContext(v: PageContextValue | null) {
  const { set } = useContext(PageCtx);
  const key = v ? v.label + "|" + v.prompt + "|" + (v.suggestions ?? []).join("|") : "";
  useEffect(() => {
    set(v);
    return () => set(null);
  }, [key]); // eslint-disable-line react-hooks/exhaustive-deps
}
