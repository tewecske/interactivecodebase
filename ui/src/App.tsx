import { useEffect, useState, type ReactNode } from "react";
import { href, navigate, useLocation, type Location } from "./router";
import { useTheme, type Theme } from "./theme";
import { Home } from "./views/Home";
import { FlowView } from "./views/Flow";
import { NodeView } from "./views/Node";
import { RouteView } from "./views/Route";
import { SiteMap } from "./views/SiteMap";

export interface ViewProps {
  params: Record<string, string>;
  theme: Theme;
}

// views maps a hash path to its view. Later issues add entries.
export const views: Record<string, { title: (p: Record<string, string>) => string; render: (v: ViewProps) => ReactNode }> = {
  home: { title: () => "Overview", render: ({ theme }) => <Home theme={theme} /> },
  node: { title: (p) => p.id?.slice(p.id.indexOf(":") + 1) ?? "Node", render: ({ params }) => <NodeView id={params.id} /> },
  sitemap: { title: () => "Site map", render: ({ params, theme }) => <SiteMap params={params} theme={theme} /> },
  route: { title: (p) => p.id ?? "Route", render: ({ params }) => <RouteView routeKey={params.id} /> },
  flow: { title: (p) => `Flow: ${p.route}`, render: ({ params, theme }) => <FlowView params={params} theme={theme} /> },
};

const nav = [
  { view: "home", label: "Overview" },
  { view: "sitemap", label: "Site map" },
  { view: "tables", label: "Tables" },
  { view: "search", label: "Search" },
];

export function App() {
  const loc = useLocation();
  const [theme, toggleTheme] = useTheme();
  const [trail, setTrail] = useState<Location[]>([]);
  const [query, setQuery] = useState("");

  // Keep a breadcrumb of the last few distinct locations.
  useEffect(() => {
    setTrail((t) => {
      const key = href(loc.view, loc.params);
      const rest = t.filter((l) => href(l.view, l.params) !== key);
      return [...rest, loc].slice(-6);
    });
  }, [loc]);

  const view = views[loc.view];
  return (
    <div className="app">
      <header>
        <a className="brand" href={href("home")}>
          icb
        </a>
        <form
          className="search"
          onSubmit={(e) => {
            e.preventDefault();
            if (query.trim()) navigate("search", { q: query.trim() });
          }}
        >
          <input
            id="global-search"
            type="search"
            placeholder="Search routes, functions, tables…  ( / )"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </form>
        <button className="theme" onClick={toggleTheme} title="Toggle light/dark">
          {theme === "dark" ? "☀" : "☾"}
        </button>
      </header>
      <nav>
        {nav.map((n) => (
          <a key={n.view} href={href(n.view)} className={loc.view === n.view ? "active" : ""}>
            {n.label}
          </a>
        ))}
      </nav>
      <main>
        <div className="breadcrumb">
          {trail.map((l, i) => {
            const t = views[l.view]?.title(l.params) ?? l.view;
            return i === trail.length - 1 ? (
              <span key={i}>{t}</span>
            ) : (
              <a key={i} href={href(l.view, l.params)}>
                {t}
              </a>
            );
          })}
        </div>
        {view ? view.render({ params: loc.params, theme }) : <p className="muted">This view is not built yet.</p>}
      </main>
    </div>
  );
}
