import { useCallback, useRef, useState } from "react";
import { MarkdownArtifactEditor } from "@/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor";
import { ArtifactRewriteDialog } from "@/modules/chat/components/WorkflowPanel/ArtifactRewriteDialog";
import type { ArtifactRewriteSelection } from "@/modules/chat/components/WorkflowPanel/ArtifactRewriteDialog";
import type {
  MarkdownRewritePreview,
} from "@/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor";
import type { MarkdownSelection } from "@/modules/chat/components/WorkflowPanel/artifactRewriteSelection";
import {
  ChatServiceApi,
  PromptServiceApi,
  type RewriteSelectionPreview,
} from "@/modules/chat/utils/request";

interface EditableBlockProps {
  value: string;
  conversationId?: string;
  historyId?: string;
  onCiteSelection?: (text: string) => void;
}

function replaceSelectedText(markdown: string, selectedText: string, replacement: string) {
  const index = markdown.indexOf(selectedText);
  if (index < 0) throw new Error("selected text is no longer present");
  return markdown.slice(0, index) + replacement + markdown.slice(index + selectedText.length);
}

/** A persisted chat writing surface backed by the shared Workflow editor and diff controls. */
export default function EditableBlock({
  value,
  conversationId,
  historyId,
  onCiteSelection,
}: EditableBlockProps) {
  const [markdown, setMarkdown] = useState(value);
  const [revision, setRevision] = useState(0);
  const [rewriteSelection, setRewriteSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [rewritePreview, setRewritePreview] = useState<MarkdownRewritePreview | null>(null);
  const persistedMarkdownRef = useRef(value);

  const save = useCallback(async (nextMarkdown: string, baseRevision: number) => {
    if (!conversationId || !historyId) throw new Error("editable message identity unavailable");
    const response = await ChatServiceApi().patchEditableBlock({
      conversation_id: conversationId,
      history_id: historyId,
      base_content: persistedMarkdownRef.current,
      content: nextMarkdown,
    }, { silentError: true } as never);
    const nextRevision = Math.max(revision, baseRevision) + 1;
    persistedMarkdownRef.current = nextMarkdown;
    setMarkdown(nextMarkdown);
    setRevision(nextRevision);
    return { markdown: nextMarkdown, revision: nextRevision };
  }, [conversationId, historyId, revision]);

  const openRewrite = useCallback((selection: MarkdownSelection) => {
    setRewriteSelection({
      type: "markdown",
      selected_text: selection.text,
      selectedText: selection.text,
      anchor: selection.anchor,
      paragraph: selection.paragraph,
      startOffset: selection.startOffset,
    });
  }, []);

  const requestRewritePreview = useCallback(async (
    instruction: string,
    selection: ArtifactRewriteSelection,
  ): Promise<RewriteSelectionPreview> => {
    const response = await PromptServiceApi().polishEditableSelection({
      content: selection.selectedText,
      user_instruct: `${instruction}\nOnly return the revised selected text. Do not add explanations or Markdown fences.`,
      allow_empty: true,
    }, {
      timeout: 10 * 60 * 1_000,
      silentError: true,
    } as never);
    const newText = response.data.content ?? "";
    const nextMarkdown = replaceSelectedText(markdown, selection.selectedText, newText);
    return {
      status: "ready",
      action: "rewrite_selection",
      base_revision: revision,
      representation: "markdown",
      target: { type: "block", block_type: "paragraph" },
      preview: { old_text: selection.selectedText, new_text: newText },
      patch: { type: "string_replace_set", payload: {} },
      artifact: { content_type: "text/markdown", value: nextMarkdown },
    };
  }, [markdown, revision]);

  const handlePreviewReady = useCallback((preview: RewriteSelectionPreview) => {
    const selection = rewriteSelection;
    if (!selection?.paragraph) return;
    const nextMarkdown = String(preview.artifact.value);
    setRewritePreview({
      paragraph: selection.paragraph,
      startOffset: selection.startOffset,
      sessionId: "",
      slotId: "",
      listIndex: 0,
      preview,
      applyPreview: async () => {
        const result = await save(nextMarkdown, preview.base_revision);
        return result.revision;
      },
    });
  }, [rewriteSelection, save]);

  return (
    <div className="md-editable-block" data-testid="editable-writing-block">
      <MarkdownArtifactEditor
        markdown={markdown}
        sourceRevision={revision}
        presentation="chat"
        onCiteSelection={onCiteSelection}
        onSave={save}
        onRewriteSelection={openRewrite}
        rewriteDialogOpen={Boolean(rewriteSelection)}
        rewritePreview={rewritePreview}
        onRewritePreviewApplied={(nextRevision) => {
          if (typeof nextRevision === "number") setRevision(nextRevision);
          setRewritePreview(null);
          setRewriteSelection(null);
        }}
        onRewritePreviewRejected={() => {
          setRewritePreview(null);
          setRewriteSelection(null);
        }}
      />
      <ArtifactRewriteDialog
        open={Boolean(rewriteSelection)}
        sessionId=""
        slotId=""
        listIndex={0}
        baseRevision={revision}
        selection={rewriteSelection}
        onClose={() => setRewriteSelection(null)}
        onApplied={() => undefined}
        onPreviewReady={handlePreviewReady}
        requestPreview={requestRewritePreview}
        terminology="edit"
      />
    </div>
  );
}
