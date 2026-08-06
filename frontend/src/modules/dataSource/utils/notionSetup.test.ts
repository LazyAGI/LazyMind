import { afterEach, describe, expect, it } from "vitest";
import { NOTION_APP_SETUP_STORAGE_KEY } from "../constants/options";
import {
  clearNotionAppSetup,
  loadNotionAppSetup,
  persistNotionAppSetup,
} from "./notionSetup";

describe("notionSetup", () => {
  afterEach(() => {
    localStorage.clear();
  });

  it("returns null when nothing is stored", () => {
    expect(loadNotionAppSetup()).toBeNull();
  });

  it("persists and loads a valid setup", () => {
    persistNotionAppSetup({ appId: " app-1 ", appSecret: " secret-1 " });
    expect(loadNotionAppSetup()).toEqual({ appId: "app-1", appSecret: "secret-1" });
  });

  it("returns null when appId or appSecret is missing", () => {
    localStorage.setItem(
      NOTION_APP_SETUP_STORAGE_KEY,
      JSON.stringify({ appId: "app-1" }),
    );
    expect(loadNotionAppSetup()).toBeNull();
  });

  it("returns null on malformed JSON", () => {
    localStorage.setItem(NOTION_APP_SETUP_STORAGE_KEY, "{bad-json");
    expect(loadNotionAppSetup()).toBeNull();
  });

  it("clears the stored setup", () => {
    persistNotionAppSetup({ appId: "app-1", appSecret: "secret-1" });
    clearNotionAppSetup();
    expect(loadNotionAppSetup()).toBeNull();
  });
});
