import { describe, expect, it } from "vitest";
import * as SelfEvolutionComponents from "./index";

describe("selfEvolution components barrel", () => {
  it("re-exports the key public component APIs", () => {
    expect(SelfEvolutionComponents.WorkflowStepCard).toBeTypeOf("function");
    expect(SelfEvolutionComponents.DatasetWorkflowStep).toBeTypeOf("function");
    expect(SelfEvolutionComponents.SelfEvolutionHomeView).toBeTypeOf("function");
    expect(SelfEvolutionComponents.SelfEvolutionWorkbenchView).toBeTypeOf("function");
    expect(SelfEvolutionComponents.HistorySessionModal).toBeTypeOf("function");
    expect(SelfEvolutionComponents.ChatMessageStream).toBeTypeOf("function");
  });
});
