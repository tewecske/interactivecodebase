import { useState, type ReactNode } from "react";
import type { FlowStep } from "../api";
import { Code } from "../components/Code";
import { KindBadge, PosLink } from "../components/Common";
import { nodeHref } from "../router";
import type { Theme } from "../theme";

// InlineFlow shows a flow as one piece of code: each function's body, and
// under every line that calls a step of the flow the callee's body, inlined
// in a box colored by depth whose header shows the call and what it runs.
export function InlineFlow({ root, theme }: { root: FlowStep; theme: Theme }) {
  return (
    <div className="inline-flow">
      {(root.children ?? []).map((c, i) => (
        <Callee key={i} step={c} call={root.node.name} depth={0} theme={theme} />
      ))}
    </div>
  );
}

// Callee is the box of one call: an interface call becomes a box per
// implementation it dispatches to.
function Callee({ step, call, depth, theme }: { step: FlowStep; call: string; depth: number; theme: Theme }) {
  if (step.node.kind === "interface_call" && step.children?.length) {
    return (
      <>
        {step.children.map((impl, i) => (
          <Block key={i} step={impl} call={call} via={step} depth={depth} theme={theme} />
        ))}
      </>
    );
  }
  return <Block step={step} call={call} depth={depth} theme={theme} />;
}

function Block({ step, call, via, depth, theme }: { step: FlowStep; call: string; via?: FlowStep; depth: number; theme: Theme }) {
  const [open, setOpen] = useState(true);
  const n = step.node;
  const p = n.pos;
  const isSink = n.kind.startsWith("sink.");
  const hasBody = !isSink && !!p?.file && !!p.startLine && !step.ref && !step.cycle;
  const from = p?.startLine ?? 0;
  const to = Math.max(p?.endLine ?? from, from);
  const branch = (via ?? step).edge.attrs?.branch;
  const calls = (via ?? step).calls;
  const note = step.cycle ? "recursive" : step.ref ? "shown above" : step.truncated ? "depth limit" : "";

  // Children called from the body go under the last line of their call,
  // the rest (no position in the body) after it.
  const children = step.children ?? [];
  const at = (c: FlowStep) => {
    const cp = c.edge.pos;
    return hasBody && cp?.file === p?.file && cp?.startLine !== undefined && cp.startLine >= from && cp.startLine <= to ? cp.startLine : undefined;
  };
  const rest = isSink ? [] : children.filter((c) => at(c) === undefined);
  const after = (line: number, src: { start: number; lines: string[] }): ReactNode => {
    const here = children.flatMap((c) => {
      const l = at(c);
      if (l === undefined) return [];
      const span = callSpan(src, l, c.edge.pos?.startCol);
      return span.end === line ? [{ c, text: span.text }] : [];
    });
    if (!here.length) return null;
    return here.map(({ c, text }, i) => <Callee key={i} step={c} call={text} depth={depth + 1} theme={theme} />);
  };

  return (
    <div className={`inline-block depth-${depth % 6}`}>
      <div className="inline-head" onClick={() => setOpen(!open)} title={open ? "Collapse" : "Expand"}>
        <span className="inline-toggle">{open ? "▾" : "▸"}</span>
        <code className="inline-call">{call}</code>
        <span className="inline-arrow">→</span>
        {via && (
          <>
            <a href={nodeHref(via.node.id)} onClick={(e) => e.stopPropagation()}>
              {via.node.name}
            </a>
            <span className="inline-arrow">→</span>
          </>
        )}
        <KindBadge kind={n.kind} />{" "}
        <a href={nodeHref(n.id)} onClick={(e) => e.stopPropagation()}>
          {n.name}
        </a>
        {calls && calls > 1 ? ` ×${calls}` : ""}
        {branch && (
          <span className="parallel" title={`runs in parallel (${(via ?? step).edge.attrs?.parallel})`}>
            {" "}∥ {branch}
          </span>
        )}
        {note && <span className="muted"> ({note})</span>}{" "}
        <span onClick={(e) => e.stopPropagation()}>
          <PosLink pos={p} />
        </span>
      </div>
      {open && (
        <div className="inline-body">
          {isSink && (
            <>
              {n.detail && <pre className="detail small">{n.detail}</pre>}
              {children.length > 0 && (
                <div className="inline-tables">
                  {children.map((t, i) => (
                    <span key={i}>
                      <KindBadge kind={`${t.edge.attrs?.op ?? ""} table`} /> <a href={nodeHref(t.node.id)}>{t.node.name}</a>{" "}
                    </span>
                  ))}
                </div>
              )}
            </>
          )}
          {hasBody && <Code file={p!.file!} from={from} to={to} marks={children.map(at).filter((l) => l !== undefined)} after={after} theme={theme} />}
          {rest.map((c, i) => (
            <Callee key={i} step={c} call={n.name} depth={depth + 1} theme={theme} />
          ))}
        </div>
      )}
    </div>
  );
}

// callSpan finds the call expression at line and col (1-based; a Go call's
// position is its opening parenthesis, a Scala call's the start of the
// callee): from the start of the callee to the parenthesis closing its
// arguments (the last list of several), continuing onto lines that chain a
// method (".name"). It
// returns the call's last line and its text on one line.
export function callSpan(src: { start: number; lines: string[] }, line: number, col?: number): { end: number; text: string } {
  const first = src.lines[line - src.start] ?? "";
  let begin = col ? Math.min(col - 1, first.length) : first.length - first.trimStart().length;
  if (first[begin] === "(") while (begin > 0 && /[\w.$]/.test(first[begin - 1])) begin--;
  let depth = 0;
  let opened = false;
  let end = line;
  const parts: string[] = [];
  for (let i = line - src.start; i < src.lines.length && end - line < 40; i++, end++) {
    const text = i === line - src.start ? first.slice(begin) : src.lines[i];
    let cut = text.length;
    for (let j = 0; j < text.length; j++) {
      const ch = text[j];
      if (ch === "(" || ch === "[") {
        depth++;
        opened = true;
      } else if (ch === ")" || ch === "]") {
        depth--;
        // Type arguments and further argument lists belong to the call.
        if (depth <= 0 && opened && text[j + 1] !== "(" && text[j + 1] !== "[") {
          cut = j + 1;
          break;
        }
      }
    }
    parts.push(text.slice(0, cut));
    if (opened && depth <= 0) break;
    const next = src.lines[i + 1]?.trimStart() ?? "";
    if (depth <= 0 && !next.startsWith(".")) break;
  }
  const joined = parts
    .join(" ")
    .replace(/\s+\./g, ".")
    .replace(/([([])\s+/g, "$1")
    .replace(/,?\s*([)\]])/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
  return { end, text: joined.length > 200 ? joined.slice(0, 199) + "…" : joined };
}
