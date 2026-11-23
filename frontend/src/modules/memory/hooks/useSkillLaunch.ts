import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { message } from 'antd';
import { useTranslation } from 'react-i18next';
import { listShowcaseCases } from '@/modules/showcase/api';
import { showcaseEntryType, showcaseTechnologyType, type ShowcaseEntryType } from '@/modules/showcase/classification';
import { CHAT_HOME_PATH } from '@/modules/chat/constants/chat';
import type { SkillLaunch } from '@/modules/chat/utils/skillLaunch';
import type { StructuredAsset } from '../shared';

export function useSkillLaunch() {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [launchingId, setLaunchingId] = useState<string>();
  const pending = useRef<AbortController>();
  useEffect(() => () => pending.current?.abort(), []);

  const launchSkill = async (skill: StructuredAsset) => {
    if (pending.current || skill.cloudResourceId || skill.isEnabled === false || skill.isAvailable === false) return;
    const controller = new AbortController();
    pending.current = controller;
    setLaunchingId(skill.id);
    try {
      let entryType: ShowcaseEntryType = 'chat';
      if (skill.originBuiltinSkillUid) {
        const { cases = [] } = await listShowcaseCases({}, { signal: controller.signal });
        if (controller.signal.aborted) return;
        const capability = cases.find(item => showcaseTechnologyType(item.type) === 'skill'
          && item.builtin_skill_uid === skill.originBuiltinSkillUid);
        if (capability) entryType = showcaseEntryType(capability.type);
      }
      const skillLaunch: SkillLaunch = { skillId: skill.id, name: skill.name, entryType };
      navigate(CHAT_HOME_PATH, { state: { skillLaunch } });
    } catch {
      if (!controller.signal.aborted) message.error(t('chat.mentionLoadFailed'));
    } finally {
      if (pending.current === controller) {
        pending.current = undefined;
        if (!controller.signal.aborted) setLaunchingId(undefined);
      }
    }
  };
  return { launchSkill, launchingId };
}
