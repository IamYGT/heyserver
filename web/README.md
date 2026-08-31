# Heyserver Web Interface

The web application is a React and TypeScript client built with Vite and
embedded into the Go panel binary. It is not deployed as a separate production
service.

## Toolchain

- Node.js 24
- npm with the committed `package-lock.json`
- React 19
- TypeScript 6
- Vite 8
- TanStack Query
- Tailwind CSS 4 and the repository's UI components
- Vitest for focused frontend tests

Install exact dependencies and start the development server:

```bash
npm ci
npm run dev
```

The Vite development server expects the Heyserver API to be available through the
configured development proxy. Authentication and host-management behavior still
come from the Go process; browser mocks are not production evidence.

## Commands

```bash
npm run lint       # ESLint
npm run typecheck  # TypeScript project references
npm test           # Vitest once
npm run build      # lint + typecheck + production bundle
```

From the repository root, `make build` installs locked frontend dependencies,
builds the interface, copies `web/dist` to `cmd/hserver/web/dist`, and compiles
the embedded Go binary.

## Source layout

- `src/pages/` — route-owned screens.
- `src/components/` — shared UI and bounded feature components.
- `src/components/servers/` — managed-node capability surfaces.
- `src/hooks/` — reusable query and browser-state behavior.
- `src/lib/api.ts` — authenticated API client and normalized API errors.
- `src/lib/` — pure feature helpers and their nearest unit tests.
- `src/routes.tsx` — browser route contract and lazy page loading.

## Product-state rules

Every data-backed surface should distinguish:

1. loading;
2. loaded but empty;
3. optional integration not configured;
4. dependency or provider unavailable;
5. permission or agent capability denied;
6. request or operation failed;
7. verified healthy or completed.

Do not convert a failed query into an empty list, a zero score, an inactive
service, or a green status. Mutating controls remain disabled until the required
server state, selected managed node, and capability are known.

Local and managed-node pages share visual patterns but not execution paths.
Remote actions always use node-scoped API routes and advertised agent
capabilities.

## Making a change

1. Put reusable, deterministic behavior in `src/lib` and add a focused Vitest
   test when practical.
2. Use the shared API client rather than direct `fetch` calls.
3. Preserve query-key scoping by selected node so local and remote data cannot
   bleed into each other.
4. Display the API's exact useful error message without exposing secrets.
5. Run the smallest relevant test and `npm run build` for a user-facing behavior
   change.
6. Refresh the embedded bundle in `cmd/hserver/web/dist` before committing.

Generated files under `web/dist` are ignored. The synchronized embedded output
under `cmd/hserver/web/dist` is committed because Go release builds embed it.
