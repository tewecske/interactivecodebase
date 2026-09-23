import { Code } from "../components/Code";
import type { Theme } from "../theme";

// CodeView shows a whole file (up to 2000 lines) with a line range marked.
export function CodeView({ params, theme }: { params: Record<string, string>; theme: Theme }) {
  const line = params.line ? Number(params.line) : undefined;
  const end = params.end ? Number(params.end) : line;
  return (
    <section>
      <h1 className="mono">{params.file}</h1>
      <p className="muted">Click an identifier to go to its definition.</p>
      <Code file={params.file} markFrom={line} markTo={end} theme={theme} scrollToMark />
    </section>
  );
}
