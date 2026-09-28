import type { WriterBlock } from './writerIR';

type OutlineInstructions = Pick<WriterBlock, 'outline_description'>;

export function writerOutlineDescription(instructions?: OutlineInstructions): string {
  return instructions?.outline_description?.trim() ?? '';
}
