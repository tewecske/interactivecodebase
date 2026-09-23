import { useEffect, useRef, useState } from "react";
import { api, type GraphNode } from "../api";
import { KindBadge } from "./Common";
import { navigate, nodeHref } from "../router";

// kindOrder ranks result groups: what users usually look for first.
const kindOrder = ["route", "sql_table", "method", "func", "type", "interface_call", "template", "sink.sql", "htmx_call", "static_asset", "sql_column"];

export const kindLabels: Record<string, string> = {
  route: "Routes",
  sql_table: "Tables",
  method: "Methods",
  func: "Functions",
  type: "Types",
  interface_call: "Interface methods",
  template: "Templates",
  "sink.sql": "SQL queries",
  "sink.http": "HTTP calls",
  "sink.file": "File access",
  "sink.smtp": "Mail",
  "sink.exec": "Processes",
  "sink.env": "Environment",
  htmx_call: "Page requests",
  static_asset: "Static assets",
  sql_column: "Columns",
};

// groupByKind orders results by kind and keeps their order within a kind.
export function groupByKind(nodes: GraphNode[]): [string, GraphNode[]][] {
  const groups = new Map<string, GraphNode[]>();
  for (const n of nodes) groups.set(n.kind, [...(groups.get(n.kind) ?? []), n]);
  const rank = (k: string) => (kindOrder.includes(k) ? kindOrder.indexOf(k) : kindOrder.length);
  return [...groups.entries()].sort((a, b) => rank(a[0]) - rank(b[0]));
}

export function go(n: GraphNode) {
  window.location.hash = nodeHref(n.id);
}

// ResultList renders grouped results with one highlighted (keyboard) item.
export function ResultList({ nodes, active, onPick }: { nodes: GraphNode[]; active: number; onPick: (n: GraphNode) => void }) {
  let i = -1;
  return (
    <div className="results">
      {groupByKind(nodes).map(([kind, list]) => (
        <div key={kind}>
          <div className="group">{kindLabels[kind] ?? kind}</div>
          {list.map((n) => {
            i++;
            const index = i;
            return (
              <a
                key={n.id}
                href={nodeHref(n.id)}
                className={`result${index === active ? " active" : ""}`}
                onMouseDown={(e) => {
                  e.preventDefault();
                  onPick(n);
                }}
                data-index={index}
              >
                <KindBadge kind={n.kind} /> <span className="name">{n.name}</span>{" "}
                <span className="muted">{n.package ?? n.pos?.file ?? ""}</span>
              </a>
            );
          })}
        </div>
      ))}
    </div>
  );
}

// flatOrder is the order ResultList shows results in, for keyboard moves.
export function flatOrder(nodes: GraphNode[]): GraphNode[] {
  return groupByKind(nodes).flatMap(([, list]) => list);
}

// SearchBox is the header search: results as you type, arrows to move,
// Enter to open, Escape to close; Enter with nothing picked opens the
// full results page.
export function SearchBox() {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<GraphNode[]>([]);
  const [active, setActive] = useState(-1);
  const [open, setOpen] = useState(false);
  const seq = useRef(0);

  useEffect(() => {
    const text = q.trim();
    if (text.length < 2) {
      setResults([]);
      return;
    }
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api.search(text, undefined, 30).then(
        (r) => mine === seq.current && (setResults(flatOrder(r)), setActive(-1)),
        () => mine === seq.current && setResults([]),
      );
    }, 150);
    return () => clearTimeout(t);
  }, [q]);

  const pick = (n: GraphNode) => {
    setOpen(false);
    go(n);
  };
  return (
    <div className="search">
      <input
        id="global-search"
        type="search"
        autoComplete="off"
        placeholder="Search routes, functions, tables, SQL…  ( / )"
        value={q}
        onChange={(e) => {
          setQ(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((a) => Math.min(a + 1, results.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((a) => Math.max(a - 1, -1));
          } else if (e.key === "Enter") {
            e.preventDefault();
            if (active >= 0 && results[active]) pick(results[active]);
            else if (q.trim()) {
              setOpen(false);
              navigate("search", { q: q.trim() });
            }
          } else if (e.key === "Escape") {
            setOpen(false);
            (e.target as HTMLInputElement).blur();
          }
        }}
      />
      {open && results.length > 0 && (
        <div className="dropdown">
          <ResultList nodes={results} active={active} onPick={pick} />
        </div>
      )}
    </div>
  );
}
