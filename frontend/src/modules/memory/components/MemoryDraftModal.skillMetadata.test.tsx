import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import MemoryDraftModal from "./MemoryDraftModal";
import { createStructuredDraft, normalizeTagValues } from "../shared";

const asset = { id: "writer", name: "writer", description: "Write papers", category: "external", tags: ["academic"], content: "# Steps", field: "writing", aliases: ["论文"], keywords: ["摘要"] };

function Editor({ onSave }: { onSave: (draft: unknown) => void }) {
  const [draft, setDraft] = useState(createStructuredDraft(asset));
  return <MemoryDraftModal t={(key: string) => key} modalOpen modalTitle="metadata" closeModal={vi.fn()}
    saveDraft={async () => onSave(draft)} activeTab="skills" glossarySaving={false} skillSaving={false}
    isReadOnly={false} draft={draft} setDraft={setDraft} pendingGlossaryMergeSourceIds={[]} modalMode="edit"
    tagOptions={[]} normalizeTagValues={normalizeTagValues} handleImportSkillPackage={vi.fn()} />;
}

describe("Skill metadata editor", () => {
  it("shows full market presentation in read-only detail while preserving source metadata", () => {
    const description = "用于分析网页和提取内容的完整中文说明".repeat(20);
    const draft = createStructuredDraft({...asset, display_name: "网页助手", display_description: description, tag_labels: {academic: "学术"}});
    render(<MemoryDraftModal t={(key: string) => key} modalOpen modalTitle="detail" closeModal={vi.fn()}
      saveDraft={vi.fn()} activeTab="skills" glossarySaving={false} skillSaving={false}
      isReadOnly draft={draft} setDraft={vi.fn()} pendingGlossaryMergeSourceIds={[]} modalMode="view"
      tagOptions={[]} normalizeTagValues={normalizeTagValues} handleImportSkillPackage={vi.fn()} />);
    expect(screen.getByDisplayValue("网页助手")).toBeInTheDocument();
    expect(screen.getByDisplayValue(description)).toBeInTheDocument();
    expect(screen.getByText("学术")).toBeInTheDocument();
    expect(draft.name).toBe("writer");
    expect(draft.content).toBe("# Steps");
  });
  it("edits the field while preserving search lists, category and execution content", async () => {
    const onSave = vi.fn();
    render(<Editor onSave={onSave} />);
    expect(screen.getByText("论文")).toBeInTheDocument();
    expect(screen.getByText("摘要")).toBeInTheDocument();
    expect(screen.getByText("academic")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("admin.memorySkillField"), { target: { value: "research" } });
    fireEvent.click(screen.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ ...asset, field: "research" })));
  });
});
