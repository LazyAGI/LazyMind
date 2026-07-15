import { lazy, Suspense } from 'react';
import { Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import type { GraphModel } from '../core/model';
import type { ValidationError } from '../core/validator';
import type { PluginModel } from '../core/pluginModel';
import type { ScenarioData } from '../ScenarioEditor';
import type { CanvasHandle } from './Canvas';

export type { CanvasHandle };

const Canvas = lazy(() => import('./Canvas'));

interface Props {
  model: GraphModel;
  errors: ValidationError[];
  onModelChange: (model: GraphModel) => void;
  pluginModel?: PluginModel;
  scenarioData?: ScenarioData;
  onScenarioChange?: (data: ScenarioData) => void;
  canvasRef?: React.Ref<CanvasHandle>;
  readonly?: boolean;
  onCreateArtifact?: () => void;
}

export default function GraphCanvas(props: Props) {
  const { t } = useTranslation();
  return (
    <Suspense
      fallback={
        <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}>
          <Spin tip={t('selfEvolutionRun.graphCanvasLoading')} />
        </div>
      }
    >
      <Canvas {...props} />
    </Suspense>
  );
}
