import { describe, expect, it } from "vitest";
import UIUtils from "./ui";

describe("UIUtils.generatePageToken", () => {
  it("returns an empty string when page is 0 (falsy)", () => {
    expect(UIUtils.generatePageToken({ page: 0, pageSize: 10, total: 100 })).toBe("");
  });

  it("encodes page/pageSize/total into a base64 JSON token", () => {
    const token = UIUtils.generatePageToken({ page: 2, pageSize: 10, total: 100 });
    const decoded = JSON.parse(atob(token));
    expect(decoded).toEqual({ Start: 20, Limit: 10, TotalCount: 100 });
  });

  it("computes Start as page * pageSize for different page sizes", () => {
    const token = UIUtils.generatePageToken({ page: 3, pageSize: 20, total: 5 });
    const decoded = JSON.parse(atob(token));
    expect(decoded.Start).toBe(60);
    expect(decoded.Limit).toBe(20);
    expect(decoded.TotalCount).toBe(5);
  });
});
