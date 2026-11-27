import type { SlotRevision } from '@/modules/chat/store/workflowPanel';

function markdownBody(slot: SlotRevision): string | undefined {
  if (slot.document?.representation !== 'markdown') return undefined;
  let value: unknown = slot.artifact_value;
  while (value && typeof value === 'object') {
    const object = value as Record<string, unknown>;
    if ('data' in object) value = object.data;
    else if ('text' in object) value = object.text;
    else return undefined;
  }
  return typeof value === 'string' && value.trim() ? value.replace(/\r\n/g, '\n').trim() : undefined;
}

/** Match source snapshots by content, never by filename or relative image path. */
export function matchingWriterSourceMaterial(source: SlotRevision, materials: SlotRevision[]): SlotRevision | undefined {
  if (source.slot_id !== 'source_document') return undefined;
  const body = markdownBody(source);
  const hash = source.document?.render_context?.source_hash;
  return materials.find(material => material.selected && material.slot_id === 'source_materials'
    && material.step_id === source.step_id
    && (hash && material.document?.render_context?.source_hash
      ? hash === material.document.render_context.source_hash
      : body !== undefined && markdownBody(material) === body));
}
