/**
 * Colours are CSS variables (RGB channels, defined in src/index.css) so the whole console can switch to dark by
 * putting `dark` on <html>. Light values equal the original hex codes, so light mode renders exactly as before.
 * `<alpha-value>` keeps opacity modifiers like `bg-crit-bg/40` working.
 *
 * Some tokens need different values per utility in dark mode (e.g. `bg-violet` behind white text stays deep violet
 * while `text-violet` on a dark surface turns light), so background / text / border colours are split below.
 */
const v = (name) => `rgb(var(--c-${name}) / <alpha-value>)`;

/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      fontFamily: {
        sans: ["'IBM Plex Sans'", "system-ui", "sans-serif"],
        mono: ["'IBM Plex Mono'", "ui-monospace", "monospace"],
      },
      colors: {
        page: v("page"),
        surface: v("surface"),
        subtle: v("subtle"),
        hover: v("hover"),
        ink: v("ink"),
        muted: v("muted"),
        line: v("line"),
        hairline: v("hairline"),
        violet: { DEFAULT: v("violet"), hover: v("violet-hover"), tint: v("violet-tint"), border: v("violet-border") },
        crit: { fg: v("crit-fg"), bg: v("crit-bg") },
        warn: { fg: v("warn-fg"), bg: v("warn-bg"), border: v("warn-border") },
        ok: { fg: v("ok-fg"), bg: v("ok-bg") },
        info: { fg: v("info-fg"), bg: v("info-bg") },
      },
      backgroundColor: {
        // Cards, inputs and buttons are `bg-white`: the surface colour.
        white: v("surface"),
        // `bg-ink` sits behind white text (user bubbles, selected toggles, log viewer): stays dark.
        ink: v("ink-bg"),
        violet: { DEFAULT: v("violet-bg"), hover: v("violet-hover-bg") },
        slate: { 50: v("slate-50-bg"), 100: v("slate-100-bg"), 200: v("slate-200-bg"), 300: v("slate-300-bg") },
      },
      textColor: {
        // Body-text greys turn light in dark mode; 100–400 are used on dark backgrounds already and stay put.
        slate: { 500: v("slate-500"), 600: v("slate-600"), 700: v("slate-700"), 800: v("slate-800"), 900: v("slate-900") },
      },
      borderColor: {
        violet: { DEFAULT: v("violet-line") },
        slate: { 200: v("slate-200-line"), 300: v("slate-300-line") },
        red: { 200: v("red-200-line") },
      },
      borderRadius: { card: "8px", btn: "6px" },
      keyframes: {
        livepulse: {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0.35" },
        },
      },
      animation: { livepulse: "livepulse 1.6s ease-in-out infinite" },
    },
  },
  plugins: [],
};
