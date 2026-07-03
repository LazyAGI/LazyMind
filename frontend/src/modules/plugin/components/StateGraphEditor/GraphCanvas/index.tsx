import { lazy, Suspense } from 'react';
import { Spin } from 'antd';
import type { GraphModel } from '../core/model';
import type { ValidationError } from '../core/validator';

const Canvas = lazy(() => import('./Canvas'));

interface Props {
  model: GraphModel;
  errors: ValidationError[];
  onModelChange: (model: GraphModel) => void;
}

export default function GraphCanvas(props: Props) {
  return (
    <Suspense
      fallback={
        <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}>
          <Spin tip="加载画布..." />
        </div>
      }
    >
      <Canvas {...props} />
    </Suspense>
  );
}
