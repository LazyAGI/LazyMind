import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useSkillLaunch } from './useSkillLaunch';
import type { StructuredAsset } from '../shared';

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), list: vi.fn(), error: vi.fn() }));
vi.mock('react-router-dom', () => ({ useNavigate: () => mocks.navigate }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('antd', () => ({ message: { error: mocks.error } }));
vi.mock('@/modules/showcase/api', () => ({ listShowcaseCases: mocks.list }));
const skill: StructuredAsset = { id: 'installed-id', name: '同名技能', category: 'external', content: '', description: '', tags: [] };
beforeEach(() => { vi.clearAllMocks(); mocks.list.mockResolvedValue({ cases: [] }); });

describe('skill list launch', () => {
  it.each(['chat', 'work'])('uses the builtin identity to resolve %s mode and retains the installed skill ID', async type => {
    mocks.list.mockResolvedValue({ cases: [
      { type: type === 'chat' ? 'work' : 'chat', builtin_skill_uid: 'other', title: skill.name },
      { type, builtin_skill_uid: 'builtin-id' },
    ] });
    const { result } = renderHook(useSkillLaunch);
    await act(async () => result.current.launchSkill({ ...skill, originBuiltinSkillUid: 'builtin-id' }));
    expect(mocks.navigate).toHaveBeenCalledWith('/agent/chat/home', { state: { skillLaunch: { skillId: skill.id, name: skill.name, entryType: type } } });
  });

  it('defaults an unclassified skill to quick chat without fetching the catalog', async () => {
    const { result } = renderHook(useSkillLaunch);
    await act(async () => result.current.launchSkill(skill));
    expect(mocks.list).not.toHaveBeenCalled();
    expect(mocks.navigate).toHaveBeenCalledWith('/agent/chat/home', { state: { skillLaunch: { skillId: skill.id, name: skill.name, entryType: 'chat' } } });
  });

  it('does not silently choose chat when classification fails', async () => {
    mocks.list.mockRejectedValue(new Error('offline'));
    const { result } = renderHook(useSkillLaunch);
    await act(async () => result.current.launchSkill({ ...skill, originBuiltinSkillUid: 'builtin-id' }));
    expect(mocks.navigate).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith('chat.mentionLoadFailed');
    expect(result.current.launchingId).toBeUndefined();
  });

  it('does not launch unavailable or cloud-only skills', async () => {
    const { result } = renderHook(useSkillLaunch);
    for (const flags of [{ cloudResourceId: 'cloud' }, { isEnabled: false }, { isAvailable: false }]) {
      await act(async () => result.current.launchSkill({ ...skill, ...flags }));
    }
    expect(mocks.navigate).not.toHaveBeenCalled();
  });

  it('ignores duplicate clicks and a classification response after leaving the page', async () => {
    let resolve!: (value: unknown) => void;
    mocks.list.mockReturnValue(new Promise(done => { resolve = done; }));
    const { result, unmount } = renderHook(useSkillLaunch);
    let pending!: Promise<void>;
    act(() => { pending = result.current.launchSkill({ ...skill, originBuiltinSkillUid: 'builtin-id' }); });
    await act(async () => result.current.launchSkill(skill));
    expect(mocks.list).toHaveBeenCalledOnce();
    unmount();
    await act(async () => { resolve({ cases: [] }); await pending; });
    expect(mocks.navigate).not.toHaveBeenCalled();
  });
});
