import { describe, expect, it } from "vitest";

import {
  getModelTypeForCapability,
  mapModelTypeToCapability,
} from "./ModelProvidersPage";

describe("custom model type mapping", () => {
  it("submits the canonical technical type for embedding models", () => {
    expect(getModelTypeForCapability("EMBEDDING")).toBe("embed");
  });

  it("maps canonical and legacy embedding types back to the embedding capability", () => {
    expect(mapModelTypeToCapability("embed")).toBe("EMBEDDING");
    expect(mapModelTypeToCapability("embedding")).toBe("EMBEDDING");
  });

  it("submits and reads the canonical lowercase type for vision-language models", () => {
    expect(getModelTypeForCapability("VLM")).toBe("vlm");
    expect(mapModelTypeToCapability("vlm")).toBe("VLM");
    expect(mapModelTypeToCapability("VLM")).toBe("VLM");
  });
});
