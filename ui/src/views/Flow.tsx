import { useEffect, useState } from "react";
import { api, type FlowStep } from "../api";
import { Code } from "../components/Code";
import { KindBadge, Loaded, PosLink } from "../components/Common";
import { MermaidView } from "../components/Mermaid";
import { href, navigate, nodeHref } from "../router";
import type { Theme } from "../theme";
import { useAsync } from "../useAsync";

const prunes = [
  { key: "sinks", label: "paths to SQL / files / APIs" },
  { key: "module", label: "all module code" },
  { key: "none", label: "everything" },
];

// FlowView shows what a route does, down to SQL, files and external
// systems: a sequence diagram with numbered steps, or the call tree.
export function FlowView({ params, theme }: { params: Record<string, string>; theme: Theme }) {
  const route = params.route;
  const isEntry = route.startsWith("entry:");
  const prune = params.prune ?? "sinks";
  const method = params.method ?? "";
  const mode = params.mode === "tree" ? "tree" : "sequence";
  const set = (p: Record<string, string | undefined>) => navigate("flow", { route, prune, method: method || undefined, mode, ...p });
  const diagram = useAsync(() => api.diagram("flow", { route, prune, method: method || undefined }), [route, prune, method]);
  const tree = useAsync(() => api.flow(route, method || undefined, prune), [route, prune, method]);
  // Expand or collapse every step's code at once; gen tells steps apart
  // clicks on the same button.
  const [allCode, setAllCode] = useState({ open: false, gen: 0 });

  return (
    <section>
      <h1>
        Flow of{" "}
        {isEntry ? (
          <a href={href("node", { id: route })}>{route.slice("entry:".length)}</a>
        ) : (
          <a href={href("route", { id: route })}>{route}</a>
        )}
      </h1>
      <div className="toolbar">
        <div className="segmented">
          <button className={mode === "sequence" ? "on" : ""} onClick={() => set({ mode: "sequence" })}>Sequence</button>
          <button className={mode === "tree" ? "on" : ""} onClick={() => set({ mode: "tree" })}>Call tree</button>
        </div>
        {mode === "tree" && (
          <div className="segmented">
            <button onClick={() => setAllCode((c) => ({ open: true, gen: c.gen + 1 }))} title="Show the code of every step, so the flow reads top to bottom">
              Expand all code
            </button>
            <button onClick={() => setAllCode((c) => ({ open: false, gen: c.gen + 1 }))}>Collapse all code</button>
          </div>
        )}
        <label>
          show{" "}
          <select value={prune} onChange={(e) => set({ prune: e.target.value })}>
            {prunes.map((p) => (
              <option key={p.key} value={p.key}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        {!isEntry && (
        <label title="Follow the branches for another request method (handlers that switch on r.Method)">
          as method{" "}
          <select value={method} onChange={(e) => set({ method: e.target.value || undefined })}>
            <option value="">route's own</option>
            {["GET", "POST", "PUT", "PATCH", "DELETE", "ANY"].map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </label>
        )}
      </div>
      {mode === "sequence" ? (
        <Loaded state={diagram}>
          {(d) => (
            <div className="flow-layout">
              <MermaidView diagram={d} theme={theme} />
              <ol className="steps">
                {Object.keys(d.ids)
                  .map(Number)
                  .sort((a, b) => a - b)
                  .map((n) => (
                    <li key={n} value={n}>
                      <a href={nodeHref(d.ids[String(n)])}>{stepLabel(d.ids[String(n)])}</a>
                    </li>
                  ))}
              </ol>
            </div>
          )}
        </Loaded>
      ) : (
        <Loaded state={tree}>
          {(t) => (
            <ul className="tree">
              {t.children?.map((c, i) => (
                <TreeStep key={i} step={c} allCode={allCode} theme={theme} />
              ))}
            </ul>
          )}
        </Loaded>
      )}
    </section>
  );
}

// stepLabel shortens a graph ID for the numbered step list.
function stepLabel(id: string): string {
  const [kind, rest] = [id.slice(0, id.indexOf(":")), id.slice(id.indexOf(":") + 1)];
  const short = rest.replace(/[\w.-]+(\/[\w.-]+)+\./g, (m) => m.slice(m.lastIndexOf("/") + 1));
  return kind.startsWith("sink.") ? `${kind} at ${rest}` : short;
}

// snippet is the source range a step's code shows: a function's body, or
// the line of a call, sink or declaration; the lines where its children are
// called are marked.
function snippet(step: FlowStep): { file: string; from: number; to: number; marks: number[] } | null {
  const p = step.node.pos;
  if (!p?.file || !p.startLine || step.node.kind === "sql_table") return null;
  const from = p.startLine;
  const to = Math.max(p.endLine ?? from, from);
  const marks = (step.children ?? [])
    .map((c) => c.edge.pos)
    .filter((cp) => cp?.file === p.file && cp.startLine !== undefined && cp.startLine >= from && cp.startLine <= to)
    .map((cp) => cp!.startLine!);
  return { file: p.file, from, to, marks: from === to ? [from] : marks };
}

function TreeStep({ step, allCode, theme }: { step: FlowStep; allCode: { open: boolean; gen: number }; theme: Theme }) {
  const n = step.node;
  const isTable = n.kind === "sql_table";
  const note = step.cycle ? " (recursive)" : step.ref ? " (see above)" : step.truncated ? " (depth limit)" : "";
  const branch = step.edge.attrs?.branch;
  const code = snippet(step);
  const [open, setOpen] = useState(allCode.open);
  useEffect(() => setOpen(allCode.open), [allCode]);
  return (
    <li>
      {code ? (
        <button className="snippet-toggle" aria-expanded={open} title={open ? "Hide code" : "Show code"} onClick={() => setOpen(!open)}>
          {open ? "▾" : "▸"}
        </button>
      ) : (
        <span className="snippet-toggle" />
      )}
      <KindBadge kind={isTable ? `${step.edge.attrs?.op ?? ""} table` : n.kind} />{" "}
      <a href={nodeHref(n.id)}>{n.name}</a>
      {step.calls && step.calls > 1 ? ` ×${step.calls}` : ""}
      {branch && (
        <span className="parallel" title={`runs in parallel (${step.edge.attrs?.parallel})`}>
          {" "}∥ {branch === "each" ? "each" : branch}
        </span>
      )}
      <span className="muted">{note}</span> {!isTable && <PosLink pos={n.pos} />}
      {n.kind.startsWith("sink.") && n.detail && <pre className="detail small">{n.detail}</pre>}
      {code && open && (
        <div className="snippet">
          <Code file={code.file} from={code.from} to={code.to} marks={code.marks} theme={theme} />
        </div>
      )}
      {step.children && step.children.length > 0 && (
        <ul>
          {step.children.map((c, i) => (
            <TreeStep key={i} step={c} allCode={allCode} theme={theme} />
          ))}
        </ul>
      )}
    </li>
  );
}
