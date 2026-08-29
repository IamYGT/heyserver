# Frontend Architecture

## Tech Stack

| Layer | Library / Version |
|-------|------------------|
| UI Framework | React 19 (StrictMode) |
| Build Tool | Vite 8 |
| Language | TypeScript (strict) |
| Styling | Tailwind CSS v4 |
| Component Library | shadcn/ui (Card, Badge, Button, Dialog, Table, Skeleton) |
| Routing | react-router-dom v7 |
| Server State | TanStack Query v5 |
| Notifications | sonner (toast) |
| Icons | lucide-react |

No Redux, no Zustand — all remote state goes through TanStack Query.

---

## Routing Setup

Entry point: `web/src/main.tsx`

All routes are wrapped in a single `ProtectedLayout` component that chains `AuthGuard → Layout → ErrorBoundary → Suspense`. Only the `Login` page and the wildcard redirect live outside this wrapper.

```
<BrowserRouter>
  <Route path="/login"         → Login (eager)
  <Route element={ProtectedLayout}>
    <Route path="/"            → Dashboard (lazy)
    ...25 more lazy routes...
  <Route path="*"              → Navigate to "/"
```

Code splitting is achieved entirely through `React.lazy()`. Every page inside `ProtectedLayout` is a separate Vite chunk. The `Suspense` fallback renders `<LoadingSpinner />` while the chunk downloads.

---

## Auth Flow

**Storage**: JWT stored in `localStorage` under the key `hserver_token`.

**`lib/api.ts` — request pipeline**:
1. Read token from localStorage → attach as `Authorization: Bearer <token>` header.
2. If the response status is `401`: clear token, show "Session expired" toast, redirect to `/login`.
3. If the response status is `403`: show "Permission denied" toast.
4. Non-ok responses throw `ApiError(status, body)`.

**`AuthGuard.tsx`** wraps all protected routes. It calls `useCurrentUser()` (`GET /api/auth/me`) on mount. If the query returns 401 (handled in `api.ts`) the user lands on `/login` automatically.

**`useAuth` hooks** (`web/src/hooks/useAuth.ts`):
- `useCurrentUser()` — TanStack Query, staleTime 5 min, retry: false.
- `useLogin()` — mutation, on success writes token + seeds query cache with user object.
- `useLogout()` — mutation, on success clears token + entire query cache, hard redirects to `/login`.

---

## API Client Pattern

File: `web/src/lib/api.ts`

```ts
export const api = {
  get:    <T>(path: string) => request<T>(path),
  post:   <T>(path: string, body?: unknown) => request<T>(path, { method: 'POST', body: JSON.stringify(body) }),
  put:    <T>(path: string, body?: unknown) => request<T>(path, { method: 'PUT',  body: JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}
```

- Base URL is `/api` (relative, proxied by nginx to the Go binary).
- All requests include `credentials: 'include'` for cookie forwarding alongside Bearer.
- Network errors are caught and surfaced as a sonner toast before re-throwing.
- Empty response bodies (e.g. 204) parse to `{}` safely.

### Frontend API contract gate

`web/scripts/verify-api-routes.mjs` compares direct `api.get`, `api.post`,
`api.put`, and `api.delete` calls with the authoritative
`internal/api/routes_manifest.go` inventory. It verifies the registered HTTP
method and route shape for statically resolvable paths, including finite string
union actions and shared helpers with adjacent `@apiRoute` JSDoc contracts.
Annotated helper arguments are expanded at each call site, so local/managed-node
alternatives and action unions must all match registered routes. Helper-built
and otherwise unresolved dynamic paths are counted and reported, but are not
presented as verified.

```bash
npm --prefix web run verify:api-routes
npm --prefix web run verify:api-routes -- --show-unverified
npm --prefix web run test:api-routes
```

The production frontend build reaches the verifier through `npm run lint`, and
CI runs the focused verifier regression suite as a separate step. Dynamic API
path helpers still require a focused behavior test at their caller. An
`@apiRoute` annotation does not replace the helper's unit test; the annotation
proves manifest compatibility while the unit test proves the helper returns the
declared local and managed-node paths.

For operations whose JSON request body has been promoted into
`docs/openapi.json`, the verifier also inspects the second frontend API argument.
It checks required and unknown fields, TypeScript value categories, finite
enums, constants, and statically visible array bounds. This payload gate is
deliberately coverage-bounded: it reports the number of frontend calls backed
by promoted request schemas and does not claim coverage for undocumented
request bodies. Promoted closed-object schemas must be paired with strict Go
JSON decoding so an ignored browser field cannot appear to succeed.
Cron creation and complete replacement are included in this promoted set; the
replacement contract requires an explicit `isActive` value so omission cannot
silently disable a scheduled task.
Firewall rule creation is also promoted, so the gate rejects UI-only field
names such as `source`; callers must send the backend's exact `from` field.
PM2 process creation is promoted with a fixed execution-mode enum and instance
bound, while process action variables use the same finite lifecycle vocabulary
as the local OpenAPI path parameter.
Docker image pulls are promoted as an exact single-field payload, and local
container action variables use the same finite enum exposed by OpenAPI.
Local database creation, literal-confirmation deletion, read-only queries, and
PGM restore are promoted as closed payloads. Query text remains capped at 64
KiB and `write_mode` is explicitly `false`; database mutations remain available
through the separately authenticated writable terminal rather than a falsely
advertised SQL editor mode.
Google Drive settings, OAuth completion, OAuth application metadata, and
restore requests are also promoted. Fixed OAuth start, disconnect, connection
test, and error-dismiss actions send no JSON body instead of a misleading empty
object; remote retention zero is an explicit disabled policy rather than a
missing value.
Snapshot repository reset is promoted as a closed destructive request: the UI
must repeat the observed installation-owned repository identity and send the
fixed `purge-snapshot-repository` confirmation. Vhost restore guidance uses the
manifest path reported by the installation rather than a provider-specific
filesystem default.
Local file backup requests likewise send only observed portable vhost
identities. The API owns the configured filesystem root and does not expose a
caller-selected `filesRoot` escape hatch.

---

## State Management

TanStack Query is the sole state solution. Configuration in `main.tsx`:

```ts
new QueryClient({
  defaultOptions: {
    queries: { retry: 1, staleTime: 10_000 }
  }
})
```

Pages use `useQuery` for reads and `useMutation` for writes. There is no global store.

`NotificationBell` (inside `Layout.tsx`) polls `GET /api/audit` every 30 seconds via `refetchInterval: 30_000` to show unread counts without a WebSocket.

---

## Theme System

File: `web/src/hooks/useTheme.ts`

- Default: `dark`.
- Persisted in `localStorage` under `hserver_theme`.
- Switching adds/removes the `light` / `dark` class on `document.documentElement`.
- Tailwind reads these classes for dark-mode variants.
- The toggle button in `Layout.tsx` header renders `<Sun>` in dark mode and `<Moon>` in light mode.

Light mode appearance is achieved through CSS class toggling — no `filter: invert()` is used; Tailwind's `dark:` prefix variants handle color differences.

---

## Component Patterns

**shadcn/ui components in use:**
- `Card` — dashboard stat panels, section containers
- `Badge` — status indicators (running/stopped, OK/error)
- `Button` — all interactive actions (variant: `ghost`, `default`, `destructive`)
- `Dialog` — confirmation modals, create/edit forms
- `Table` — list views (domains, mail accounts, DNS records, etc.)
- `Skeleton` — loading placeholders while queries are pending

**Custom components:**
- `ErrorBoundary` — class component, wraps all lazy pages inside `ProtectedLayout`
- `LoadingSpinner` — shown as `Suspense` fallback during chunk downloads
- `PageSkeleton` — full-page skeleton used inside individual pages while data loads
- `EmptyState` — zero-data placeholder with icon + message
- `CommandPalette` — Cmd+K quick navigation, lives in `Layout`
- `Breadcrumb` — auto-generated from `routeLabels` map in `Layout.tsx`

The writable terminal has an additional access-state boundary before its
WebSocket component is mounted. It waits for `/api/auth/me`, opens a shell only
for a verified `admin`, and renders account loading, account verification
failure, explicit permission denial, managed-node observation failure, offline,
and missing terminal capability as separate states. Permission and node
observation failures have their own retry controls; command, clear, tab, paste,
and mobile-key controls remain disabled until both access and target readiness
are known. This prevents an HTTP `403` WebSocket handshake from appearing as an
endless generic reconnect loop.

---

## Page List (31 protected routes)

| Route | Page Component | Section |
|-------|---------------|---------|
| `/` | Dashboard | Infrastructure |
| `/domains` | Domains | Infrastructure |
| `/domains/:name` | DomainDetail | Infrastructure |
| `/nginx` | Nginx | Infrastructure |
| `/ssl` | SSL | Infrastructure |
| `/php` | PHP | Infrastructure |
| `/pm2` | PM2 | Infrastructure |
| `/monitoring` | Monitoring | Monitoring |
| `/servers` | Servers | Infrastructure |
| `/uptime` | Uptime | Monitoring |
| `/logs` | Logs | Monitoring |
| `/mail` | Mail | Monitoring |
| `/dns` | DNS | Monitoring |
| `/cloudflare` | Cloudflare | Monitoring |
| `/webmail` | Webmail (external link) | Webmail |
| `/firewall` | Firewall | Tools |
| `/files` | Files | Tools |
| `/disk` | DiskManagement | Tools |
| `/databases` | DatabasePage | Tools |
| `/backups` | Backups | Tools |
| `/cron` | Cron | Tools |
| `/docker` | Docker | Tools |
| `/deploy` | Deploy | Tools |
| `/terminal` | TerminalPage | Tools |
| `/security` | Security | Tools |
| `/notifications` | Notifications | Tools |
| `/users` | UsersPage | Admin |
| `/audit` | AuditPage | Admin |
| `/settings` | SettingsPage | Admin |
| `/about` | AboutPage | Admin |
| `/developer/api` | DeveloperAPI | Admin / contributor tools |

Navigation is defined in `Layout.tsx` as `navSections` plus `adminNavItems`. The
Webmail entry uses `external: true` and renders as `<a target="_blank">` instead
of a router `<Link>`. Developer API loads the installation-owned
`/openapi.json` contract and supports route, method, access, and tag filtering.

---

## Code Splitting

- Eager chunks: `Login`, `Layout`, `ErrorBoundary`, `AuthGuard`, `LoadingSpinner` — loaded on first paint.
- Lazy chunks: protected page components each become a separate Vite chunk,
  downloaded on first navigation.
- Bundle size and chunk hashes are build outputs; `make sync-dist` refreshes the
  embedded production assets after route or page changes.

---

## Error Boundary

`ErrorBoundary` is a React class component that catches render-time errors thrown by lazy pages or their children. It renders a fallback UI (error message + retry button) instead of crashing the whole app. It is positioned inside `ProtectedLayout`, so the sidebar and header remain visible even when a page crashes.

`LoadingSpinner` serves double duty: it is the `Suspense` fallback for chunk loading and can also be used directly in pages as an inline loading indicator.

---

## Sidebar Behaviour

- Desktop: collapsible to icon-only mode (56px wide), state persisted in `localStorage` under `hserver_sidebar_collapsed`.
- Mobile: full-width overlay sheet, opens via hamburger button, closes on backdrop click or swipe-left (threshold: 60px).
- Active route highlighted with blue left border + blue text (`bg-blue-600/10 text-blue-400`).
- Collapsed icon mode shows hover tooltips via CSS opacity transition.
- Server hostname and uptime are shown in the sidebar footer via `useSystemStats()`.
