import { describe, expect, it } from "vitest";
import {
  settingsModelTarget,
  settingsReturnTo,
  settingsRouteParams,
} from "./settingsRouteContext";

describe("settings route context", () => {
  it("accepts supported model targets only", () => {
    expect(settingsModelTarget("image_generator")).toBe("image_generator");
    expect(settingsModelTarget("unknown")).toBeUndefined();
  });

  it("accepts chat return routes and rejects external-looking routes", () => {
    expect(settingsReturnTo("/agent/chat/home/conversation-1"))
      .toBe("/agent/chat/home/conversation-1");
    expect(settingsReturnTo("//example.com/agent/chat/home")).toBeUndefined();
    expect(settingsReturnTo("/settings")).toBeUndefined();
  });

  it("keeps return and highlight context while switching model tabs", () => {
    const current = new URLSearchParams({
      section: "models",
      target: "image_generator",
      provider_id: "openai",
      return_to: "/agent/chat/home/conversation-1",
    });
    expect(settingsRouteParams(current, {
      section: "models",
      view: "providers",
    }).toString()).toBe(
      "section=models&view=providers&return_to=%2Fagent%2Fchat%2Fhome%2Fconversation-1&target=image_generator&provider_id=openai",
    );
  });
});
