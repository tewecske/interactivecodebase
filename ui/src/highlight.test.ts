import { describe, expect, it } from "vitest";
import { langFor, tokenize } from "./highlight";

describe("highlight", () => {
  it("maps Scala and sbt files to the Scala grammar", () => {
    expect(langFor("modules/backend/src/main/scala/app/Main.scala")).toBe("scala");
    expect(langFor("build.sbt")).toBe("scala");
    expect(langFor("script.sc")).toBe("scala");
  });

  it("colors Scala keywords", async () => {
    const lines = await tokenize('object Main:\n  def run(x: Int): String = "hi"', "scala", "light");
    const def = lines[1].find((t) => t.content === "def");
    const str = lines[1].find((t) => t.content.includes("hi"));
    expect(def?.color).toBeDefined();
    expect(str?.color).toBeDefined();
    expect(def?.color).not.toBe(str?.color);
  });
});
