// A small hash router: every view is a deep link like #/route?id=...
import { useEffect, useState } from "react";

export interface Location {
  view: string;
  params: Record<string, string>;
}

function parse(hash: string): Location {
  const [path, query = ""] = hash.replace(/^#\/?/, "").split("?", 2);
  return { view: path || "home", params: Object.fromEntries(new URLSearchParams(query)) };
}

export function href(view: string, params: Record<string, string | undefined> = {}): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
  return `#/${view}${q.size ? `?${q}` : ""}`;
}

export function navigate(view: string, params: Record<string, string | undefined> = {}) {
  window.location.hash = href(view, params);
}

export function useLocation(): Location {
  const [loc, setLoc] = useState(() => parse(window.location.hash));
  useEffect(() => {
    const onChange = () => setLoc(parse(window.location.hash));
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return loc;
}

// nodeHref links to the best view for a graph node.
export function nodeHref(id: string): string {
  const kind = id.slice(0, id.indexOf(":"));
  switch (kind) {
    case "route":
      return href("route", { id: id.slice("route:".length) });
    case "page":
      return href("route", { id });
    case "sql_table":
      return href("table", { name: id.slice("sql_table:".length) });
    case "entry":
      return href("flow", { route: id });
    default:
      return href("node", { id });
  }
}
