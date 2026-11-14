export interface DocumentChatSelection {
  question?: string;
  quickExplain?: boolean;
  source: "pdf" | "segment" | "paper_guide";
  guideRef?: {revision_id:string;section_key:string;block_id:string;start:number;end:number;language:string};
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
