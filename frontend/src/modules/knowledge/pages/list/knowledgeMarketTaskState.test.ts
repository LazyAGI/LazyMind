import { describe, expect, it } from "vitest";

import {
  isKnowledgeMarketTaskCompleted,
  isKnowledgeMarketTaskTerminal,
} from "./knowledgeMarketTaskState";

describe("knowledgeMarketTaskState", () => {
  it("does not finish an install while its aggregate progress is below 100", () => {
    const task = {
      jobType: "knowledge_market_install",
      jobStatus: "succeeded",
      stage: "done",
      overallPercent: 86,
    };

    expect(isKnowledgeMarketTaskTerminal(task)).toBe(false);
    expect(isKnowledgeMarketTaskCompleted(task)).toBe(false);
  });

  it("finishes an install when the aggregate progress reaches 100", () => {
    expect(
      isKnowledgeMarketTaskCompleted({
        jobType: "knowledge_market_install",
        jobStatus: "succeeded",
        stage: "done",
        overallPercent: 100,
      }),
    ).toBe(true);
  });

  it("keeps failed tasks terminal but not successfully completed", () => {
    const task = {
      jobType: "knowledge_market_install",
      jobStatus: "failed",
      stage: "failed",
      overallPercent: 42,
    };

    expect(isKnowledgeMarketTaskTerminal(task)).toBe(true);
    expect(isKnowledgeMarketTaskCompleted(task)).toBe(false);
  });
});
