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
