import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  WriterArtifactContent,
  WRITER_ARTIFACT_SLOT_IDS,
  unwrapArtifactPayload,
} from "./writerArtifactViews";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/i18n", () => ({
  default: {
    t: (key: string, opts?: Record<string, unknown>) =>
      opts ? `${key}:${JSON.stringify(opts)}` : key,
  },
}));

vi.mock("@/modules/chat/components/MarkdownViewer", () => ({
  default: ({ children }: { children: string }) => (
    <div data-testid="markdown-viewer">{children}</div>
  ),
}));

describe("WRITER_ARTIFACT_SLOT_IDS", () => {
  it("includes the known writer plugin slot ids", () => {
    expect(WRITER_ARTIFACT_SLOT_IDS.has("outline")).toBe(true);
    expect(WRITER_ARTIFACT_SLOT_IDS.has("draft_document")).toBe(true);
    expect(WRITER_ARTIFACT_SLOT_IDS.has("unknown_slot")).toBe(false);
  });
});

describe("unwrapArtifactPayload", () => {
  it("unwraps a { data: ... } envelope", () => {
    expect(unwrapArtifactPayload({ data: { foo: "bar" } })).toEqual({ foo: "bar" });
  });

  it("returns the raw value unchanged when there is no data envelope", () => {
    expect(unwrapArtifactPayload({ foo: "bar" })).toEqual({ foo: "bar" });
    expect(unwrapArtifactPayload("plain string")).toBe("plain string");
    expect(unwrapArtifactPayload(null)).toBeNull();
  });
});

describe("WriterArtifactContent", () => {
  it("renders the writing task query and key metadata fields", () => {
    render(
      <WriterArtifactContent
        slotId="writing_task"
        data={{ query: "Write a report", task_type: "report", language: "en" }}
      />,
    );
    expect(screen.getByText("Write a report")).toBeInTheDocument();
  });

  it("shows an empty state for outline slots with no nodes", () => {
    render(<WriterArtifactContent slotId="outline" data={{ nodes: [] }} />);
    expect(screen.getByText("chat.writer.noOutline")).toBeInTheDocument();
  });

  it("renders outline node cards with titles for populated outlines", () => {
    render(
      <WriterArtifactContent
        slotId="outline"
        data={{ nodes: [{ node_id: "n1", title: "Introduction" }] }}
      />,
    );
    expect(screen.getByText("Introduction")).toBeInTheDocument();
  });

  it("renders draft document content through the markdown viewer", () => {
    render(
      <WriterArtifactContent
        slotId="draft_document"
        data={{ title: "Doc Title", sections: [{ title: "Sec 1", content: "Body text" }] }}
      />,
    );
    expect(screen.getByText("Doc Title")).toBeInTheDocument();
    expect(screen.getByTestId("markdown-viewer")).toHaveTextContent("Body text");
  });

  it("shows a pass/fail badge and issues for review reports", () => {
    render(
      <WriterArtifactContent
        slotId="review_report"
        data={{
          result: {
            is_passed: false,
            score: 62,
            summary: "Needs work",
            issues: [{ category: "clarity", severity: "high", description: "Unclear intro" }],
          },
        }}
      />,
    );
    expect(screen.getByText("chat.writer.failed")).toBeInTheDocument();
    expect(screen.getByText("Needs work")).toBeInTheDocument();
    expect(screen.getByText("Unclear intro")).toBeInTheDocument();
  });

  it("unwraps a { data } envelope before rendering writing_output content", () => {
    render(
      <WriterArtifactContent
        slotId="writing_output"
        data={{ data: { content: "Final output text" } }}
      />,
    );
    expect(screen.getByTestId("markdown-viewer")).toHaveTextContent("Final output text");
  });

  it("shows an empty state for writing_output when there is no content", () => {
    render(<WriterArtifactContent slotId="writing_output" data={{}} />);
    expect(screen.getByText("chat.writer.noFinalContent")).toBeInTheDocument();
  });

  it("falls back to a generic structured view for unknown slot ids", () => {
    render(<WriterArtifactContent slotId="some_other_slot" data={{ foo: "bar value" }} />);
    expect(screen.getByText("bar value")).toBeInTheDocument();
  });
});
