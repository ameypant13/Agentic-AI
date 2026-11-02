import { useCallback, useEffect, useState } from "react";

/**
 * Light / dark theme: a `dark` class on <html> switches the colour tokens in index.css.
 * The user's choice is kept in localStorage; with no choice the system preference wins (and is followed live).
 */
const KEY = "aic.theme";

function stored(): "dark" | "light" | null {
  try {
    const v = window.localStorage.getItem(KEY);
    return v === "dark" || v === "light" ? v : null;
  } catch {
    return null;
  }
}

function systemDark(): boolean {
  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    return false;
  }
}

function apply(dark: boolean) {
  document.documentElement.classList.toggle("dark", dark);
}

function initial(): boolean {
  const s = stored();
  return s ? s === "dark" : systemDark();
}

// Apply as soon as the app's modules load, before the first render, so there is no light flash.
if (typeof document !== "undefined") apply(initial());

export function useTheme() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"));

  useEffect(() => {
    let mq: MediaQueryList;
    try {
      mq = window.matchMedia("(prefers-color-scheme: dark)");
    } catch {
      return;
    }
    const onChange = (e: MediaQueryListEvent) => {
      if (stored()) return;
      apply(e.matches);
      setDark(e.matches);
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  const toggle = useCallback(() => {
    setDark((prev) => {
      const next = !prev;
      apply(next);
      try {
        window.localStorage.setItem(KEY, next ? "dark" : "light");
      } catch {
        // storage blocked: the choice lasts for this page load only
      }
      return next;
    });
  }, []);

  return { dark, toggle };
}
