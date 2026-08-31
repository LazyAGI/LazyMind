export interface DocumentChatSelection {
  source: "pdf" | "segment";
  text: string;
  page?: number;
  bbox?: [number, number, number, number];
  segmentId?: string;
  segmentNumber?: number;
  group?: string;
}
