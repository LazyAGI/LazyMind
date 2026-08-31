import { describe, expect, it } from "vitest";
import type { ConversationArtifact } from "@/modules/chat/store/taskCenter";
import {
  appendDownloadParam,
  conversationHasFileIdLink,
  findArtifactByFileId,
  getArtifactFilename,
  getArtifactSignSource,
  getArtifactTextContent,
  getFileIdFromHref,
  isBrowserDownloadHref,
  isInlineDownloadableArtifact,
  normalizeArtifactFileLinks,
} from "./artifactLinks";

function artifact(
  overrides: Partial<ConversationArtifact> & { artifact_id: string },
): ConversationArtifact {
  return {
    slot: "output",
    content_type: "file",
    seq: 1,
    value: {},
    conversation_id: "conv-1",
    history_id: "hist-1",
    producer_type: "main_agent",
    ...overrides,
  };
}

describe("getFileIdFromHref", () => {
  it("parses a file_id href", () => {
    expect(getFileIdFromHref("file_id:abc-123")).toBe("abc-123");
  });

  it("parses sanitized-safe artifact hash hrefs", () => {
    expect(getFileIdFromHref("#artifact-file-abc-123")).toBe("abc-123");
  });

  it("rejects other protocols", () => {
    expect(getFileIdFromHref("https://example.com/file")).toBe("");
    expect(getFileIdFromHref("file_id:")).toBe("");
  });
});

describe("normalizeArtifactFileLinks", () => {
  it("normalizes file_id hrefs to sanitizer-safe hash hrefs", () => {
    expect(
      normalizeArtifactFileLinks("请下载 [notes.txt](file_id: abc-1 )"),
    ).toBe("请下载 [notes.txt](#artifact-file-abc-1)");
  });
});

describe("conversationHasFileIdLink", () => {
  it("detects a file_id markdown link", () => {
    expect(
      conversationHasFileIdLink("附件：[report.docx](file_id:artifact-1)"),
    ).toBe(true);
    expect(conversationHasFileIdLink("没有附件")).toBe(false);
  });
});

describe("artifact lookup helpers", () => {
  const fileArtifact = artifact({
    artifact_id: "art-1",
    filename: "report.docx",
    value: {
      filename: "report.docx",
      path: "/data/subagent/chat-artifacts/report.docx",
      url: "/static-files/chat-artifacts/report.docx?expires=1&sig=abc",
    },
  });
  const textArtifact = artifact({
    artifact_id: "art-2",
    content_type: "text",
    filename: "notes.txt",
    value: { text: "hello" },
  });

  it("finds artifacts by id aliases", () => {
    expect(findArtifactByFileId([fileArtifact], "art-1")).toBe(fileArtifact);
    expect(
      findArtifactByFileId(
        [artifact({ artifact_id: "other", value: { file_id: "nested" } })],
        "nested",
      )?.artifact_id,
    ).toBe("other");
  });

  it("uses url or path as the sign source for file artifacts", () => {
    expect(getArtifactSignSource(fileArtifact)).toContain("/static-files/");
    expect(getArtifactSignSource(textArtifact)).toBe("");
    expect(
      getArtifactSignSource(
        artifact({
          artifact_id: "path-only",
          value: { path: "/data/subagent/out.pdf" },
        }),
      ),
    ).toBe("/data/subagent/out.pdf");
  });

  it("extracts inline text for blob downloads", () => {
    expect(isInlineDownloadableArtifact(textArtifact)).toBe(true);
    expect(getArtifactTextContent(textArtifact)).toBe("hello");
    expect(getArtifactFilename(fileArtifact)).toBe("report.docx");
  });
});

describe("appendDownloadParam", () => {
  it("adds download=1 once", () => {
    expect(appendDownloadParam("https://host/file")).toBe(
      "https://host/file?download=1",
    );
    expect(appendDownloadParam("https://host/file?sig=1")).toBe(
      "https://host/file?sig=1&download=1",
    );
    expect(appendDownloadParam("https://host/file?download=1")).toBe(
      "https://host/file?download=1",
    );
  });
});

describe("isBrowserDownloadHref", () => {
  it("accepts signed or blob URLs and rejects workspace paths", () => {
    expect(isBrowserDownloadHref("https://host/api/core/static-files/a")).toBe(
      true,
    );
    expect(isBrowserDownloadHref("blob:https://host/1")).toBe(true);
    expect(
      isBrowserDownloadHref("/data/subagent/chat-artifacts/report.docx"),
    ).toBe(false);
  });
});
