// Parses the Mermaid diagrams the Go server generates (golden files from
// internal/server tests) with the real Mermaid parser, so syntax errors in
// the generators fail the UI test suite.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import mermaid from "mermaid";
import { describe, expect, it } from "vitest";

const dir = join(import.meta.dirname, "..", "..", "testdata", "golden", "mermaid");
const files = readdirSync(dir).filter((f: string) => f.endsWith(".mmd"));

describe("golden Mermaid diagrams parse", () => {
  it("found golden files", () => expect(files.length).toBeGreaterThan(0));
  for (const file of files) {
    it(file, async () => {
      const text = readFileSync(join(dir, file), "utf8");
      await expect(mermaid.parse(text)).resolves.toBeTruthy();
    });
  }
});
