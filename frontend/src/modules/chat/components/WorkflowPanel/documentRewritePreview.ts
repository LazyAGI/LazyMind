import type { RewriteSelectionPreview } from '@/modules/chat/utils/request';

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('invalid preview');
  return value as Record<string, unknown>;
}

export function documentRewritePreview(value: unknown, revision: number, draft?: number): RewriteSelectionPreview {
  const data = record(value);
  if (!Array.isArray(data.results) || data.results.length === 0) throw new Error('expected rewrite targets');
  const parsed = data.results.map((item)=>parseResult(data,record(item)));
  return {...parsed[0],base_revision:revision,base_draft_version:draft,results:parsed.map(({target,preview,patch})=>({target,preview,patch}))};
}

function parseResult(data: Record<string,unknown>, item: Record<string,unknown>): RewriteSelectionPreview {
  const target = record(item.target), preview = record(item.preview);
  const patch = record(item.patch), artifact = record(data.artifact), commit = record(data.commit);
  const representation = data.representation;
  if ((representation !== 'markdown' && representation !== 'ir') || target.type !== 'block'
    || typeof target.block_type !== 'string' || typeof preview.old_text !== 'string' || typeof preview.new_text !== 'string'
    || patch.type !== (representation === 'markdown' ? 'string_replace_set' : 'writer_ir_patch')
    || typeof artifact.content_type !== 'string' || typeof commit.token !== 'string' || !/^[0-9a-f]{32}$/.test(commit.token)) throw new Error('invalid preview');
  const body = representation === 'markdown'
    ? (typeof artifact.value === 'string' ? artifact.value : undefined)
    : record(artifact.value);
  if (body === undefined) throw new Error('invalid candidate');
  const nodeID = typeof target.node_id === 'string' ? target.node_id : undefined;
  const start = typeof target.target_start === 'number' ? target.target_start : undefined;
  const end = typeof target.target_end === 'number' ? target.target_end : undefined;
  if (representation === 'ir' ? !nodeID : !Number.isInteger(start) || !Number.isInteger(end) || start! < 0 || end! <= start!) throw new Error('invalid target');
  return {
    status:'ready', action:'rewrite_selection', base_revision:0, representation,
    target:{type:'block',block_type:target.block_type,node_id:nodeID,target_start:start,target_end:end},
    preview:{old_text:preview.old_text,new_text:preview.new_text},
    patch:{type:representation === 'markdown' ? 'string_replace_set' : 'writer_ir_patch',payload:record(patch.payload)},
    artifact:{content_type:artifact.content_type,value:body},commit:{token:commit.token},
  };
}
