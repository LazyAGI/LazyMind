import { lazy, Suspense } from 'react';
import { Spin } from 'antd';
import type { ValidationError } from '../core/validator';

const Editor = lazy(() => import('./Editor'));

interface Props {
  value: string;
  onChange: (value: string) => void;
  errors: ValidationError[];
}

export default function YamlEditor(props: Props) {
  return (
    <Suspense fallback={<div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}><Spin tip="加载编辑器..." /></div>}>
      <Editor {...props} />
    </Suspense>
  );
}
