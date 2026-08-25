import { describe, expect, it } from "vitest";
import {
  clearCloudKnowledgeCreateParams,
  getCloudKnowledgeCreatePath,
  getCloudKnowledgeCreateProvider,
  isCloudKnowledgeCreateRequest,
} from "./cloudDocumentKnowledge";

describe("cloud document knowledge creation context", () => {
  it("carries a supported provider into the knowledge creation route", () => {
    const path = getCloudKnowledgeCreatePath("notion");

    expect(path).toBe(
      "/lib/knowledge/list?createSource=cloud-documents&provider=notion",
    );
    expect(getCloudKnowledgeCreateProvider(path.split("?")[1])).toBe("notion");
  });

  it("rejects unsupported providers instead of opening the wrong wizard", () => {
    const search = "?createSource=cloud-documents&provider=googledrive";

    expect(isCloudKnowledgeCreateRequest(search)).toBe(true);
    expect(getCloudKnowledgeCreateProvider(search)).toBeNull();
  });

  it("consumes only cloud-document creation parameters", () => {
    expect(
      clearCloudKnowledgeCreateParams(
        "?createSource=cloud-documents&provider=feishu&tab=mine",
      ),
    ).toBe("?tab=mine");
  });
});
