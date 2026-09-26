import { describe, expect, it } from "vitest";
import { callSpan } from "./InlineFlow";

describe("callSpan", () => {
  const src = (start: number, ...lines: string[]) => ({ start, lines });

  it("backs up from a Go call's parenthesis to the callee", () => {
    expect(callSpan(src(10, "\tn, err := h.store.Notes.Create(ctx, note) // x"), 10, 32)).toEqual({ end: 10, text: "h.store.Notes.Create(ctx, note)" });
  });

  it("follows a Scala call over chained lines and its arguments", () => {
    const s = src(530, "      pageAndTotal <-", "        repo", "          .listAllGamesPage(", "            nameContains,", "            favoritesOf,", "          )", "          .orDie <&> repo.count(x)");
    expect(callSpan(s, 531, 9)).toEqual({ end: 535, text: "repo.listAllGamesPage(nameContains, favoritesOf)" });
  });

  it("keeps type arguments and further argument lists", () => {
    expect(callSpan(src(1, "    ZIO.serviceWithZIO[GameService](_.allGames(a, b))"), 1, 5).text).toBe("ZIO.serviceWithZIO[GameService](_.allGames(a, b))");
    expect(callSpan(src(1, "  ctx.run(q)(using ec) { x =>"), 1, 3).text).toBe("ctx.run(q)(using ec)");
  });

  it("takes the rest of the line without a column", () => {
    expect(callSpan(src(1, "    doIt", "    other()"), 1)).toEqual({ end: 1, text: "doIt" });
  });
});
