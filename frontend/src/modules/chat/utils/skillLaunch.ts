import type { ShowcaseEntryType } from '@/modules/showcase/classification';

/** Select a skill in a fresh composer without submitting a message. */
export interface SkillLaunch {
  skillId: string;
  name: string;
  entryType: ShowcaseEntryType;
}

export function readSkillLaunch(state: unknown): SkillLaunch | undefined {
  const launch = (state as { skillLaunch?: Partial<SkillLaunch> } | null)?.skillLaunch;
  if (typeof launch?.skillId !== 'string' || !launch.skillId
    || typeof launch.name !== 'string' || !launch.name
    || (launch.entryType !== 'chat' && launch.entryType !== 'work')) return;
  return { skillId: launch.skillId, name: launch.name, entryType: launch.entryType };
}
