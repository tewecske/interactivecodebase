import { useEffect, useRef, useState, type ReactNode } from "react";
import type { ThemedToken } from "@shikijs/types";
import { api, type Ref } from "../api";
import { langFor, tokenize } from "../highlight";
import { href } from "../router";
import type { Theme } from "../theme";
import { useAsync } from "../useAsync";

// Code shows lines [from, to] of a module file, highlighted, with the
// lines in [markFrom, markTo] marked and identifiers linked to their
// definitions.
export function Code({
  file,
  from = 1,
  to,
  markFrom,
  markTo,
  theme,
  scrollToMark,
}: {
  file: string;
  from?: number;
  to?: number;
  markFrom?: number;
  markTo?: number;
  theme: Theme;
  scrollToMark?: boolean;
}) {
  const source = useAsync(() => api.source(file, from, to), [file, from, to]);
  const refs = useAsync(() => (file.endsWith(".go") ? api.refs(file) : Promise.resolve([] as Ref[])), [file]);
  const [tokens, setTokens] = useState<ThemedToken[][] | null>(null);
  const marked = useRef<HTMLDivElement>(null);

  const lines = !source.loading && "data" in source ? source.data.lines : null;
  useEffect(() => {
    if (!lines) return;
    let live = true;
    tokenize(lines.join("\n"), langFor(file), theme).then((t) => live && setTokens(t));
    return () => {
      live = false;
    };
  }, [lines, file, theme]);
  useEffect(() => {
    if (scrollToMark && tokens) marked.current?.scrollIntoView?.({ block: "center" });
  }, [tokens, scrollToMark]);

  if (source.loading) return <p className="muted">Loading source…</p>;
  if ("error" in source) return <p className="error">{source.error.message}</p>;
  const start = source.data.start;
  const byLine = new Map<number, Ref[]>();
  if (!refs.loading && "data" in refs) for (const r of refs.data) byLine.set(r.line, [...(byLine.get(r.line) ?? []), r]);

  return (
    <div className="code">
      {source.data.lines.map((text, i) => {
        const n = start + i;
        const isMarked = markFrom !== undefined && n >= markFrom && n <= (markTo ?? markFrom);
        const isFirstMark = n === markFrom;
        return (
          <div key={n} ref={isFirstMark ? marked : undefined} className={`line${isMarked ? " marked" : ""}`}>
            <a className="ln" href={href("code", { file, line: String(n) })}>
              {n}
            </a>
            <span className="src">{renderLine(text, tokens?.[i], byLine.get(n) ?? [])}</span>
          </div>
        );
      })}
      {source.data.end < source.data.total && to === undefined && <p className="muted">… file truncated at {source.data.end} lines</p>}
    </div>
  );
}

// renderLine splits highlighted tokens at identifier boundaries and turns
// identifiers with a definition into links.
function renderLine(text: string, tokens: ThemedToken[] | undefined, refs: Ref[]): ReactNode {
  const toks = tokens ?? [{ content: text, offset: 0 } as ThemedToken];
  const ranges = refs.map((r) => ({ start: charIndex(text, r.col - 1), end: charIndex(text, r.endCol - 1), ref: r }));
  const out: ReactNode[] = [];
  let col = 0;
  toks.forEach((tok, ti) => {
    const tStart = col;
    const tEnd = col + tok.content.length;
    col = tEnd;
    let pos = tStart;
    const cuts = ranges.filter((r) => r.end > tStart && r.start < tEnd).sort((a, b) => a.start - b.start);
    const style = tok.color ? { color: tok.color } : undefined;
    for (const [ci, r] of cuts.entries()) {
      const s = Math.max(r.start, tStart);
      const e = Math.min(r.end, tEnd);
      if (s > pos) out.push(<span key={`${ti}-${ci}-a`} style={style}>{text.slice(pos, s)}</span>);
      const target = r.ref.target;
      out.push(
        <a
          key={`${ti}-${ci}-r`}
          className="ref"
          style={style}
          title={r.ref.node ? r.ref.node : `${r.ref.kind} ${r.ref.name}`}
          href={target ? href("code", { file: target.file!, line: String(target.startLine) }) : href("node", { id: r.ref.node! })}
        >
          {text.slice(s, e)}
        </a>,
      );
      pos = e;
    }
    if (pos < tEnd) out.push(<span key={`${ti}-z`} style={style}>{text.slice(pos, tEnd)}</span>);
  });
  return out;
}

// charIndex converts a byte offset in a line (Go positions) to a UTF-16
// index (JavaScript strings).
function charIndex(line: string, byteOffset: number): number {
  let bytes = 0;
  for (let i = 0; i < line.length; i++) {
    if (bytes >= byteOffset) return i;
    const c = line.codePointAt(i)!;
    bytes += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
    if (c >= 0x10000) i++;
  }
  return line.length;
}
