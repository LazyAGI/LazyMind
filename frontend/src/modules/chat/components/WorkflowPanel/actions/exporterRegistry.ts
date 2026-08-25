import type { ComponentType } from 'react';
import type {
  TabDef,
  WorkflowSession,
  WorkflowTabAction,
} from '@/modules/chat/store/workflowPanel';
import { PresentationExportAction } from '../ppt/PresentationExportAction';

export interface ExportActionProviderProps {
  action: WorkflowTabAction;
  tab: TabDef;
  session: WorkflowSession;
  rows: number[];
}

const exportActionProviders: Record<string, ComponentType<ExportActionProviderProps>> = {
  'html-presentation': PresentationExportAction,
};

export function resolveExportActionProvider(
  providerId: string,
): ComponentType<ExportActionProviderProps> | undefined {
  return exportActionProviders[providerId];
}
