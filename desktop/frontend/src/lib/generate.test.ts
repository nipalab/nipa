import { describe, expect, it } from "vitest";
import { addTrailer, withGeneratedTrailer } from "./generate";

describe("addTrailer", () => {
  it("appends a trailer paragraph like aiprompt.AddTrailer", () => {
    expect(addTrailer("feat: a\n\n", "Nipa-Generated", " qwen ")).toBe("feat: a\n\nNipa-Generated: qwen");
    expect(addTrailer("", "Nipa-Generated", "qwen")).toBe("Nipa-Generated: qwen");
    expect(addTrailer("x", "Nipa-Generated", " ")).toBe("x");
    expect(addTrailer("x\n\nNipa-Generated: other", "Nipa-Generated", "qwen")).toBe("x\n\nNipa-Generated: other");
  });

  it("only marks text that came from a draft", () => {
    expect(withGeneratedTrailer("fix: b", "qwen")).toBe("fix: b\n\nNipa-Generated: qwen");
    expect(withGeneratedTrailer("fix: b", null)).toBe("fix: b");
    expect(withGeneratedTrailer("  ", "qwen")).toBe("  ");
  });
});
