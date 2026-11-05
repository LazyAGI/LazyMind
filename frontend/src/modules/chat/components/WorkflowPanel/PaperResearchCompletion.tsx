import { Alert } from 'antd';
import { useTranslation } from 'react-i18next';
import type { PaperInvestigation } from '@/modules/knowledge/api/paperWorkflows';

export default function PaperResearchCompletion({ completion }: { completion: PaperInvestigation['completion'] }) {
  const { t } = useTranslation();
  if (!completion) return null;
  const incomplete = completion.status === 'incomplete';
  return <Alert showIcon type={incomplete ? 'warning' : 'success'} style={{ marginBottom: 16 }}
    message={t(`paperTasks.researchCompletion.${completion.status}`)}
    description={incomplete ? t(`paperTasks.researchCompletion.${completion.reason}`) : undefined} />;
}
