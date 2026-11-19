export interface DocumentChatSelection {
  question?: string;
  quickExplain?: boolean;
  source: "pdf" | "segment";
  text: string;
  context?: string;
  page?: number;
  bbox?: [number, number, number, number];
  segmentId?: string;
  segmentNumber?: number;
  group?: string;
}

export interface DocumentTranslationRequest {
  id: number;
  selection: DocumentChatSelection;
}
