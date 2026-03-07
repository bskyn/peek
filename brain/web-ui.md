# Web UI

## Stack

- React 19 + Vite 7 + Tailwind CSS v4 (`@tailwindcss/vite` plugin)
- `react-diff-view` + `diff` for unified diff rendering
- oxlint + oxfmt for lint/format
- `babel-plugin-react-compiler` for automatic memoization

## Theme

Catppuccin Mocha palette. Inter font (sans), SF Mono (mono).

## Structure

```
web/
  src/
    app/App.tsx           — main shell, wires hooks together
    components/           — TimelineCard, SessionCard, EmptyPanel, DiffBlock
    hooks/                — useRouter, useSessions, useSessionDetail
    lib/                  — api, stream, types, format helpers
    styles/app.css        — Tailwind import + Catppuccin theme vars
  index.html              — entry point, font loading
  vite.config.ts          — tailwind + react-compiler plugins
```

Build output goes to `internal/viewer/dist/` (embedded in Go binary).
