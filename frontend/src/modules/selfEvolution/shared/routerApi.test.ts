import { describe, expect, it } from "vitest";

import { normalizeRouterAlgorithm } from "./routerApi";

const algorithm = {
  algorithm_id: "evo_20260813_example",
  status: "active",
  healthy_instances: 1,
  instance_count: 1,
};

describe("normalizeRouterAlgorithm", () => {
  it("maps the current top-level thread_id response into the owner used by the UI", () => {
    expect(normalizeRouterAlgorithm({
      ...algorithm,
      thread_id: "thread-current-contract",
    })?.owner.thread_id).toBe("thread-current-contract");
  });

  it("keeps supporting a nested owner and gives it precedence", () => {
    expect(normalizeRouterAlgorithm({
      ...algorithm,
      thread_id: "thread-current-contract",
      owner: { thread_id: "thread-legacy-contract" },
    })?.owner.thread_id).toBe("thread-legacy-contract");
  });

  it("keeps the default algorithm thread empty when the API returns null", () => {
    expect(normalizeRouterAlgorithm({
      ...algorithm,
      algorithm_id: "default",
      thread_id: null,
    })?.owner.thread_id).toBe("");
  });
});
