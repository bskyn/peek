import { useCallback, useEffect, useState } from "react";

export type AppRoute = { kind: "home" } | { kind: "session"; sessionID: string };

function parseRoute(pathname: string): AppRoute {
  const parts = pathname.split("/").filter(Boolean);
  if (parts[0] === "sessions" && parts[1] != null) {
    try {
      return { kind: "session", sessionID: decodeURIComponent(parts[1]) };
    } catch {
      return { kind: "home" };
    }
  }
  return { kind: "home" };
}

function routeToPath(route: AppRoute): string {
  switch (route.kind) {
    case "home":
      return "/";
    case "session":
      return `/sessions/${encodeURIComponent(route.sessionID)}`;
  }
}

export function useRouter() {
  const [route, setRoute] = useState<AppRoute>(() => parseRoute(window.location.pathname));

  useEffect(() => {
    const handlePopState = () => setRoute(parseRoute(window.location.pathname));
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  const navigate = useCallback((nextRoute: AppRoute) => {
    const targetPath = routeToPath(nextRoute);
    if (targetPath !== window.location.pathname) {
      window.history.pushState({}, "", targetPath);
    }
    setRoute(nextRoute);
  }, []);

  const selectedSessionID = route.kind === "session" ? route.sessionID : "";

  return { route, navigate, selectedSessionID };
}
