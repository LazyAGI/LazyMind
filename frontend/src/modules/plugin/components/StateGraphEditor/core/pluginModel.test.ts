import { describe, expect, it } from "vitest";
import {
  SLOT_COMPATIBLE_WIDGETS,
  SLOT_DEFAULT_WIDGET,
  createEmptyPluginModel,
} from "./pluginModel";

describe("createEmptyPluginModel", () => {
  it("returns a plugin model with empty id/name and empty steps/slots", () => {
    expect(createEmptyPluginModel()).toEqual({ id: "", name: "", steps: [], slots: [] });
  });
});

describe("SLOT_DEFAULT_WIDGET", () => {
  it("maps each slot type/cardinality combination to its default widget", () => {
    expect(SLOT_DEFAULT_WIDGET["text/single"]).toBe("text-single");
    expect(SLOT_DEFAULT_WIDGET["text/list"]).toBe("text-list");
    expect(SLOT_DEFAULT_WIDGET["image/list"]).toBe("image-gallery");
    expect(SLOT_DEFAULT_WIDGET["json/single"]).toBe("json-block");
  });
});

describe("SLOT_COMPATIBLE_WIDGETS", () => {
  it("lists text-markdown as compatible with text slots but not image slots", () => {
    expect(SLOT_COMPATIBLE_WIDGETS["text/single"]).toContain("text-markdown");
    expect(SLOT_COMPATIBLE_WIDGETS["image/single"]).not.toContain("text-markdown");
  });

  it("allows json slots to fall back to a plain text widget", () => {
    expect(SLOT_COMPATIBLE_WIDGETS["json/single"]).toContain("text-single");
  });
});
