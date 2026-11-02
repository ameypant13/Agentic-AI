# Frontend development

The UI is a React 18 single-page app built with Vite 5, TypeScript and Tailwind CSS 3, in `ai-console/web`. It talks
only to the console's own `/api`. Markdown in Copilot replies is rendered with `react-markdown` and `remark-gfm`.

## Run it

```bash
cd ai-console/web
npm install
npm run dev        # http://localhost:5173, proxies /api to http://localhost:8090
```

Run the API alongside it ([Running locally](local.md)). To serve the built UI from the Go server instead:

```bash
npm run build      # → web/dist; the server's STATIC_DIR
```

`npm run build` (`tsc -b && vite build`) is also the type check. `tsc --noEmit` alone always passes here.

## Structure

| Path | What it holds |
|---|---|
| `src/main.tsx` | Mounts the app inside the router and the page-context provider. |
| `src/App.tsx` | Routes. `/login` is outside the layout; every other page is inside `Layout`. |
| `src/components/Layout.tsx` | Header, sidebar, read-only banner, the Copilot dock, the issues poll (every 30 s), <kbd>Cmd/Ctrl</kbd>+<kbd>K</kbd>. Loads `/api/me` and redirects to `/login?next=…` on `401`. |
| `src/components/shell/nav.ts` | Sidebar groups. The command palette lists the same pages. |
| `src/components/shell/CommandPalette.tsx` | Fuzzy search over clusters, pages and certificates, and "Ask Copilot". |
| `src/components/shell/CopilotDock.tsx` | The docked Copilot panel. |
| `src/components/ClusterShell.tsx` | The cluster explorer's rail, resource tree and header. |
| `src/pages/*.tsx` | One file per page. |
| `src/api/client.ts` | `fetch` wrappers for every endpoint, and the SSE reader for chat streams. |
| `src/api/types.ts` | Types matching `API.md`. |
| `src/lib/useApi.ts` | Data-loading hook. |
| `src/lib/me.tsx` | The signed-in user (`isAdmin`, `readOnly`, `claudeKey`, `controlHub`). |
| `src/lib/pageContext.tsx` | Lets a page give the Copilot dock a context label, prompt and suggestions. |

## Conventions

- **Admin-only UI** checks `me.isAdmin`. The server enforces every admin check again; the UI only hides what would
  fail.
- **Read-only**: `me.readOnly` drives the banner and the action cards.
- **URL state**: filters, tabs and selections go in the query string (`?ns=`, `?q=`, `?cluster=`, `?view=`), so views
  can be shared and survive reloads.
- **Polling**: pages refresh their own data in the background (for example pods every 15 s, issues every 30 s) and
  pause while the browser tab is hidden.
- **Streaming**: chat replies use `fetch` with a `ReadableStream` reader, because `EventSource` cannot POST. Events
  are `step`, `step_done`, `text`, `action`, `investigation`, `done` and `error`
  ([API](../api/copilot.md#stream-events)).
- **Local storage** holds only per-browser conveniences: the theme (`aic.theme`), the dock state (`aic.dock`), the
  collapsed tree sections (`aic.clusterTree.collapsed`) and the wide table toggle (`aic.resourceList.wide`).

## Adding a page

1. Add the API call to `src/api/client.ts` and its types to `src/api/types.ts` (and the route to `API.md`).
2. Create `src/pages/MyPage.tsx`.
3. Add the route in `src/App.tsx`.
4. Add it to the sidebar in `src/components/shell/nav.ts` (the command palette picks it up).
5. If the Copilot dock should know about the page, call `usePageContext({label, prompt, suggestions})` from the page.
6. Run `npm run build`.
7. Add a page to the [User guide](../user-guide/getting-started.md).

!!! note
    `usePageContext` exists and the dock reads it, but no page calls it yet, so the dock always shows "No page
    context".
