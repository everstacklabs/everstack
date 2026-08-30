import { StrictMode } from 'react'
import ReactDOM from 'react-dom/client'
import { RouterProvider, createRouter } from '@tanstack/react-router'

import * as TanStackQueryProvider from './integrations/tanstack-query/root-provider.tsx'
import { initPostHog, capturePageview } from './lib/posthog'

// Import the generated route tree
import { routeTree } from './routeTree.gen'
import '@everstack/ui/styles.css';
import './styles/styles.css';

import { EverstackThemeProvider } from '@everstack/ui/components'

import reportWebVitals from './reportWebVitals.ts'

// Create a new router instance

const TanStackQueryProviderContext = TanStackQueryProvider.getContext()
const router = createRouter({
  routeTree,
  context: {
    ...TanStackQueryProviderContext,
  },
  defaultPreload: 'intent',
  scrollRestoration: true,
  defaultStructuralSharing: true,
  defaultPreloadStaleTime: 0,
  stringifySearch: (search) => {
    const params = new URLSearchParams()
    Object.entries(search as Record<string, unknown>).forEach(([key, value]) => {
      if (value === undefined || value === null) return
      // Avoid JSON stringifying primitives; use plain strings instead
      params.set(key, String(value))
    })
    const qs = params.toString()
    return qs ? `?${qs}` : ''
  },
  parseSearch: (search) => {
    const params = new URLSearchParams(search as string)
    const obj: Record<string, string> = {}
    params.forEach((value, key) => {
      obj[key] = value
    })
    return obj
  },
})

initPostHog()
router.subscribe('onResolved', ({ toLocation }) => {
  capturePageview(toLocation.href)
})

// Handle trailing slash redirects on the client side
if (typeof window !== 'undefined') {
  const currentPath = window.location.pathname;
  if (currentPath.endsWith('/') && currentPath !== '/') {
    const newPath = currentPath.slice(0, -1);
    window.history.replaceState(null, '', newPath + window.location.search);
    window.location.reload();
  }
}

// Recover from stale-tab chunk-load failures after a deploy. Vite emits
// `vite:preloadError` when a dynamic import (e.g. shell-terminal.tsx) maps
// to a hashed chunk filename that no longer exists on the CDN — the server
// then falls back to index.html and the browser refuses to execute HTML
// as a module. Force a single reload to pick up the new chunk manifest.
// sessionStorage gate prevents a reload loop if the chunk is genuinely
// missing (build artifact problem rather than a deploy race).
if (typeof window !== 'undefined') {
  const RELOAD_KEY = 'evs:chunk-reload-at'
  const RELOAD_WINDOW_MS = 10_000
  window.addEventListener('vite:preloadError', (event) => {
    const last = Number(sessionStorage.getItem(RELOAD_KEY) ?? '0')
    if (Date.now() - last < RELOAD_WINDOW_MS) {
      // We already reloaded recently; the chunk really is missing.
      // Let React's error boundary surface the failure instead of
      // looping.
      console.error('chunk preload failed twice in a row, not reloading', event)
      return
    }
    sessionStorage.setItem(RELOAD_KEY, String(Date.now()))
    window.location.reload()
  })
}

// Register the router instance for type safety
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

// Render the app
const rootElement = document.getElementById('app')
if (rootElement && !rootElement.innerHTML) {
  const root = ReactDOM.createRoot(rootElement)
  root.render(
    <StrictMode>
      <EverstackThemeProvider>
        <TanStackQueryProvider.Provider {...TanStackQueryProviderContext}>
          <RouterProvider router={router} />
        </TanStackQueryProvider.Provider>
      </EverstackThemeProvider>
    </StrictMode>,
  )
}

// If you want to start measuring performance in your app, pass a function
// to log results (for example: reportWebVitals(console.log))
// or send to an analytics endpoint. Learn more: https://bit.ly/CRA-vitals
reportWebVitals()
