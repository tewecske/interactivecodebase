import type { ReactNode } from "react";
import type { Async } from "../useAsync";
import type { Pos } from "../api";
import { href } from "../router";

// Loaded renders an async value: a spinner, an error, or the content.
export function Loaded<T>({ state, children }: { state: Async<T>; children: (data: T) => ReactNode }) {
  if (state.loading) return <p className="muted">Loading…</p>;
  if ("error" in state) return <p className="error">{state.error.message}</p>;
  return <>{children(state.data)}</>;
}

// PosLink links a source position to the code view.
export function PosLink({ pos }: { pos?: Pos }) {
  if (!pos?.file) return null;
  const line = pos.startLine ?? 1;
  return (
    <a className="pos" href={href("code", { file: pos.file, line: String(line), end: pos.endLine ? String(pos.endLine) : undefined })}>
      {pos.file}:{line}
    </a>
  );
}

export function AccessBadge({ access, optional }: { access?: string; optional?: boolean }) {
  if (!access) return null;
  const text = optional ? "public · session-aware" : access;
  return <span className={`badge access-${optional ? "optional" : access}`}>{text}</span>;
}

export function KindBadge({ kind }: { kind: string }) {
  return <span className={`badge kind kind-${kind.replace(".", "-")}`}>{kind}</span>;
}
