import { describe, expect, it } from "vitest";
import {
  CLOUD_DATA_SOURCE_OAUTH_CHANNEL,
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
} from "./types";

describe("oauth/types constants", () => {
  it("defines the feishu oauth broadcast channel name", () => {
    expect(FEISHU_DATA_SOURCE_OAUTH_CHANNEL).toBe(
      "lazymind:datasource:feishu-oauth",
    );
  });

  it("aliases the cloud channel to the feishu channel for backward compatibility", () => {
    expect(CLOUD_DATA_SOURCE_OAUTH_CHANNEL).toBe(FEISHU_DATA_SOURCE_OAUTH_CHANNEL);
  });
});
