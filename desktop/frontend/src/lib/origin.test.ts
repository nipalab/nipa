import { describe, expect, it } from "vitest";
import { originShortId, originSystemName } from "./origin";

const sha = "0123456789abcdef0123456789abcdef01234567";

describe("origin", () => {
  it.each([
    [`git:default:${sha}`, "Git", "0123456"],
    [`hg:default:${sha}`, "Mercurial", "0123456"],
    ["svn:repo:1234:trunk", "SVN", "r1234"],
    ["p4:depot:58211://depot/main", "Perforce", "CL 58211"],
    ["cvs:src:abc", "cvs", "abc"],
    ["git:default", "", ""],
    ["", "", ""],
    [undefined, "", ""],
  ])("formats %s", (origin, system, short) => {
    expect(originSystemName(origin)).toBe(system);
    expect(originShortId(origin)).toBe(short);
  });
});
