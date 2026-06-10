export interface PluginEvent {
  type: string;
  plugin_session_id?: string;
  plugin_id?: string;
  step_id?: string;
  step_mode?: 'human' | 'auto';
  step_exec_id?: string;
  artifact_id?: string;
  value?: unknown;
  progress?: number;
  message?: string;
  error?: string;
  initial_state?: Record<string, unknown>;
  // Wrapper type from Go SSE sender
  data?: PluginEvent;
}

export interface PluginSessionState {
  sessionId: string;
  pluginId: string;
  currentStep: string;
  artifacts: Record<string, unknown>;
  stepProgress: { progress: number; message: string } | null;
  isWaiting: boolean;
  stepError: string | null;
}

export interface PluginContext {
  plugin_session_id: string;
  plugin_id: string;
  step: string;
  advance: boolean;
}
