import { describe, expect, it } from "vitest";
import * as SelfEvolutionPage from "./SelfEvolutionPage";
import { SelfEvolutionPageController as ControllerFromHook } from "../hooks/useSelfEvolutionPageController";

describe("SelfEvolutionPage re-exports", () => {
  it("re-exports SelfEvolutionPageController from the controller hook", () => {
    expect(SelfEvolutionPage.SelfEvolutionPageController).toBe(ControllerFromHook);
  });
});
