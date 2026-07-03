import { lazy, Suspense } from 'react';
import { Spin } from 'antd';
import type { GraphModel } from '../core/model';
import type { ValidationError } from '../core/validator';
import type { CanvasHandle } from './Canvas';

export type { CanvasHandle };

const Canvas = lazy(() => import('./Canvas'));

interface Props {
  model: GraphModel;
  errors: ValidationError[];
  onModelChange: (model: GraphModel) => void;
  canvasRef?: React.Ref<CanvasHandle>;
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
