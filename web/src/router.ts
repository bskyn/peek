import { createRootRoute, createRoute, createRouter } from '@tanstack/react-router';

import { App } from './app/App';

const rootRoute = createRootRoute({
  component: App,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
});

export const sessionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sessions/$sessionId',
});

export const runtimeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/r/$runtimeId',
});

export const runtimeSessionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/r/$runtimeId/sessions/$sessionId',
});

const routeTree = rootRoute.addChildren([indexRoute, sessionRoute, runtimeRoute, runtimeSessionRoute]);

export const router = createRouter({ routeTree });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
