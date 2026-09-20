import { createHash, webcrypto } from 'node:crypto';
import { beforeEach, expect, it, vi } from 'vitest';
import { writerPreviewMarkdown } from './writerSourceSyntax';

beforeEach(() => { vi.stubGlobal('crypto', webcrypto); });
function context(source: string) {
 return {source_hash:createHash('sha256').update(source).digest('hex'),code_fences:[],images:[]};
}
it('uses explicit source ranges for duplicate fences and Unicode prefixes', async () => {
 const fence='```text\nA --> B\n```\n';
 const source='😀\n'+fence+fence;
 const hints={...context(source),code_fences:[{start:2,end:2+fence.length,language:'mermaid'},{start:2+fence.length,end:2+2*fence.length,language:'mermaid'}]};
 expect(await writerPreviewMarkdown(source,hints)).toBe(source.split('```text').join('```mermaid'));
});
it('adapts two occurrences of one image with distinct dimensions',async()=>{
 const image='![image](image.png)';
 const source=image+'\n\n'+image;
 const hints={...context(source),images:[{start:0,end:image.length,width:100},{start:image.length+2,end:source.length,width:300,height:200}]};
 const output=await writerPreviewMarkdown(source,hints);
 expect(output).toContain('width="100"');
 expect(output).toContain('width="300" height="200"');
 expect(source).toBe(image+'\n\n'+image);
});
it('does not apply stale hints or consume private provider metadata',async()=>{
 const source='```text\nA --> B\n```\n';
 const hints={...context(source),code_fences:[{start:0,end:source.length,language:'mermaid'}]};
 expect(await writerPreviewMarkdown('New\n'+source,hints)).toBe('New\n'+source);
 expect(await writerPreviewMarkdown(source,{meta:{lazymind_provider_sync:{target_document:{meta:{github_writer_code_fences:[{source:source.replace('text','mermaid'),display:source}]}}}}} as never)).toBe(source);
});
