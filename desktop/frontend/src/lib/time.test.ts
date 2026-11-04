import { describe, expect, it } from "vitest";
import { timeAgo } from "./time";

describe("timeAgo", () => {
  const now = Date.parse("2026-11-04T12:00:00Z");
  it("describes recent and older instants", () => {
    expect(timeAgo("2026-11-04T11:59:40Z", now)).toBe("just now");
    expect(timeAgo("2026-11-04T11:55:00Z", now)).toBe("5 min ago");
    expect(timeAgo("2026-11-04T09:00:00Z", now)).toBe("3 h ago");
    expect(timeAgo("2026-11-03T11:00:00Z", now)).toBe("1 day ago");
    expect(timeAgo("2026-10-25T12:00:00Z", now)).toBe("10 days ago");
    expect(timeAgo("2026-01-01T12:00:00Z", now)).toBe(new Date("2026-01-01T12:00:00Z").toLocaleDateString());
    expect(timeAgo("2026-11-05T12:00:00Z", now)).toBe("just now");
  });

  it("returns nothing for unknown times", () => {
    expect(timeAgo("", now)).toBe("");
    expect(timeAgo("not a date", now)).toBe("");
    expect(timeAgo("2026-11-04T11:00:00Z")).not.toBe("");
  });
});
