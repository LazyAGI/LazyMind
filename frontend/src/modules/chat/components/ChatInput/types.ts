import type { RefObject } from "react";
import type { RcFile } from "antd/es/upload";
import type { ImageUploadImperativeProps } from "../ImageUpload";
import type { ThinkingDepth } from "@/modules/chat/store/chatThink";
import type { ChatMention } from "./MentionEditor";
import type { ChatConfig } from "../ChatConfigs";

export interface ChatFileList {
  uid: string;
  name: string;
  base64: string;
  previewUrl?: string;
  suffix: string;
  size: string;
}

export interface SendMessageParams {
  text: string;
  /** Preserve the resource selection when the welcome composer unmounts. */
  chatConfigSnapshot?: ChatConfig;
  mentions?: ChatMention[];
  citeMessage?: string;
  citeMessages?: string[];
  citeHistoryIds?: (string | undefined)[];
  clearInput?: boolean;
  fileList?: ChatFileList[];
  fileListRef?: RefObject<ImageUploadImperativeProps | null>;
  files?: (RcFile & { uri: string })[];
  create_time?: string;
  thinking_depth?: ThinkingDepth;
  run_in_background?: boolean;
  ask_answers_structured?: import("@/modules/chat/components/AskCard").AskAnswersStructured;
}

export interface ChatInputImperativeProps {
  clearFiles: () => void;
  element: HTMLDivElement | null;
  focus: () => void;
  uploadFiles: (files: File[]) => void;
}
