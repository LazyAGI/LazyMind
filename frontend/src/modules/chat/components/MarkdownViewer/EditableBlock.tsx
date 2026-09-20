import { markdownSelectionRange, markdownRewriteTargets } from '@/modules/chat/components/WorkflowPanel/writerMarkdownSource';
import { useCallback, useMemo, useRef, useState } from "react";
import { MarkdownArtifactEditor } from "@/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor";
import { ArtifactRewriteDialog } from "@/modules/chat/components/WorkflowPanel/ArtifactRewriteDialog";
import type { ArtifactRewriteSelection } from "@/modules/chat/components/WorkflowPanel/ArtifactRewriteDialog";
import type {
  MarkdownRewritePreview,
} from "@/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor";
import type { MarkdownSelection } from "@/modules/chat/components/WorkflowPanel/artifactRewriteSelection";
import {
  type ChatSource,
  findSourceByCitationId,
  getSourceCitationId,
  getSourceFaviconUrl,
  getSourceHref,
  getSourceLabel,
  getSourceSubtitle,
  openSource,
} from "@/modules/chat/utils/sourceAdapter";
import {
  ChatServiceApi,
  PromptServiceApi,
  type RewriteSelectionPreview,
} from "@/modules/chat/utils/request";

interface EditableBlockProps {
  value: string;
  conversationId?: string;
  historyId?: string;
  sources?: ChatSource[];
  onCiteSelection?: (text: string) => void;
}

function jsOffsetFromCodePoints(value: string, offset: number) {
  return Array.from(value).slice(0, offset).join("").length;
}

/** A persisted chat writing surface backed by the shared Workflow editor and diff controls. */
export default function EditableBlock({
  value,
  conversationId,
  historyId,
  sources = [],
  onCiteSelection,
}: EditableBlockProps) {
  const [markdown, setMarkdown] = useState(value);
  const [revision, setRevision] = useState(0);
  const [rewriteSelection, setRewriteSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [rewritePreview, setRewritePreview] = useState<MarkdownRewritePreview | null>(null);
  const persistedMarkdownRef = useRef(value);
  const currentDraftRef=useRef(value), previewBaseline=useRef(value);

  const save = useCallback(async (nextMarkdown: string, baseRevision: number) => {
    if (!conversationId || !historyId) throw new Error("editable message identity unavailable");
    await ChatServiceApi().patchEditableBlock({
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
      paragraphs: selection.paragraphSelections?.map(item => item.paragraph),
      startOffset: selection.startOffset,
      sourceRange: selection.sourceRange,
      sourceRanges: selection.sourceRanges,
    });
  }, []);

  const openSourceReference = useCallback((citationId: string) => {
    const source = findSourceByCitationId(sources, citationId);
    if (source) openSource(source);
  }, [sources]);

  const sourceReferences = useMemo(() => sources.map((source) => ({
    citationId: getSourceCitationId(source),
    faviconUrl: getSourceFaviconUrl(source),
    href: getSourceHref(source),
    label: getSourceSubtitle(source).replace(/^www\./i, "") || getSourceLabel(source),
    title: getSourceLabel(source),
  })), [sources]);

  const requestRewritePreview = useCallback(async (
    instruction: string,
    selection: ArtifactRewriteSelection,
  ): Promise<RewriteSelectionPreview> => {
    previewBaseline.current=markdown;
    const ranges = selection.sourceRanges ?? [selection.sourceRange ?? markdownSelectionRange(markdown, selection)];
    const response = await PromptServiceApi().polishEditableSelection({
      content: selection.selectedText,
      user_instruct: instruction,
      allow_empty: true,
      full_content: markdown,
      selection_ranges: ranges.map(range=>({start:range.start,end:range.end,content:range.selected_text})),
    }, {
      timeout: 10 * 60 * 1_000,
      silentError: true,
    } as never);
    if (!response.data.results?.length) throw new Error("Missing paragraph rewrite results");
    const results=[...response.data.results].sort((a,b)=>a.target_start-b.target_start);
    const expected=markdownRewriteTargets(markdown,ranges);
    if(expected.length!==results.length)throw new Error('Incomplete paragraph result');
    let nextMarkdown='',cursor=0;
    const items=results.map((result,index)=>{
      const from=jsOffsetFromCodePoints(markdown,result.target_start),to=jsOffsetFromCodePoints(markdown,result.target_end);
      if(!Number.isInteger(result.target_start)||!Number.isInteger(result.target_end)||result.target_start<0||result.target_end>Array.from(markdown).length
        ||from<cursor||to<=from||markdown.slice(from,to)!==result.old_content
        ||result.target_start!==expected[index].start||result.target_end!==expected[index].end)throw new Error('Invalid paragraph result');
      nextMarkdown+=markdown.slice(cursor,from)+result.content;cursor=to;
      return {target:{type:'block' as const,block_type:expected[index].kind,target_start:result.target_start,target_end:result.target_end},
        preview:{old_text:result.old_content,new_text:result.content},patch:{type:'string_replace_set' as const,payload:{}}};
    });
    nextMarkdown+=markdown.slice(cursor);
    return {status:'ready',action:'rewrite_selection',base_revision:revision,representation:'markdown',...items[0],results:items,
      artifact:{content_type:'text/markdown',value:nextMarkdown}};

  }, [markdown, revision]);

  const handlePreviewReady = useCallback((preview: RewriteSelectionPreview) => {
    const selection = rewriteSelection;
    if (!selection?.paragraph) return;
    const nextMarkdown = String(preview.artifact.value);
    setRewritePreview({
      paragraph: selection.paragraph,
      paragraphs: selection.paragraphs,
      sourceMarkdown: previewBaseline.current,
      startOffset: 0,
      sessionId: "",
      slotId: "",
      listIndex: 0,
      preview,
      applyPreview: async () => {
        if(persistedMarkdownRef.current!==previewBaseline.current || currentDraftRef.current!==previewBaseline.current)throw new Error('Document changed after preview');
        const result = await save(nextMarkdown, preview.base_revision);
        return result.revision;
      },
    });
  }, [rewriteSelection, save]);

  return (
    <div className="md-editable-block" data-testid="editable-writing-block">
      <MarkdownArtifactEditor
        markdown={markdown}
        allowMultipleParagraphs
        onContentChange={(content)=>{currentDraftRef.current=content;}}
        sourceRevision={revision}
        presentation="chat"
        onCiteSelection={onCiteSelection}
        onOpenSourceReference={openSourceReference}
        sourceReferences={sourceReferences}
        onSave={save}
        onRewriteSelection={rewriteSelection || rewritePreview ? undefined : openRewrite}
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
