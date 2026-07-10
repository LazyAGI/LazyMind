import { lazy, Suspense } from 'react';
import { Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import type { ValidationError } from '../core/validator';

const Editor = lazy(() => import('./Editor'));

interface Props {
  value: string;
  onChange: (value: string) => void;
  errors: ValidationError[];
  language?: string;
  readOnly?: boolean;
}

export default function YamlEditor(props: Props) {
  const { t } = useTranslation();
  return (
    <Suspense fallback={<div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}><Spin tip={t('selfEvolutionRun.yamlEditorLoading')} /></div>}>
      <Editor {...props} />
    </Suspense>
  );
}
