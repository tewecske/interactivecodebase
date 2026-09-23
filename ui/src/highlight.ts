// Syntax highlighting with Shiki, loaded lazily with only the grammars and
// themes the UI needs and the JavaScript regex engine (no WebAssembly).
import type { HighlighterCore, ThemedToken } from "@shikijs/types";
import type { Theme } from "./theme";

let highlighter: Promise<HighlighterCore> | null = null;

function load(): Promise<HighlighterCore> {
  highlighter ??= (async () => {
    const [{ createHighlighterCore }, { createJavaScriptRegexEngine }] = await Promise.all([
      import("shiki/core"),
      import("shiki/engine/javascript"),
    ]);
    return createHighlighterCore({
      themes: [import("@shikijs/themes/github-light"), import("@shikijs/themes/github-dark")],
      langs: [
        import("@shikijs/langs/go"),
        import("@shikijs/langs/html"),
        import("@shikijs/langs/sql"),
        import("@shikijs/langs/css"),
        import("@shikijs/langs/javascript"),
        import("@shikijs/langs/scala"),
      ],
      engine: createJavaScriptRegexEngine(),
    });
  })();
  return highlighter;
}

export function langFor(file: string): string {
  const ext = file.slice(file.lastIndexOf(".") + 1).toLowerCase();
  return { go: "go", html: "html", tmpl: "html", gohtml: "html", sql: "sql", css: "css", js: "javascript", mjs: "javascript", scala: "scala", sc: "scala", sbt: "scala" }[ext] ?? "text";
}

// tokenize returns the colored tokens of each line.
export async function tokenize(code: string, lang: string, theme: Theme): Promise<ThemedToken[][]> {
  if (lang === "text") return code.split("\n").map((content) => [{ content, offset: 0 }]);
  const h = await load();
  return h.codeToTokensBase(code, { lang, theme: theme === "dark" ? "github-dark" : "github-light" });
}
