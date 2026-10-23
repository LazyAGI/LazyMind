import {
  AudioOutlined,
  MessageOutlined,
  OrderedListOutlined,
  PictureOutlined,
  QuestionCircleOutlined,
  SoundOutlined,
  VideoCameraOutlined,
} from "@ant-design/icons";
import type { SVGProps } from "react";
import { useTranslation } from "react-i18next";
import { getModelTaskType } from "../modelTaskTypes";
import type { ProviderModel } from "../pages/ModelProvidersPage";
import "./ModelTaskTabs.scss";

function EmbeddingOutlined(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      {...props}
      className="anticon"
      width="1em"
      height="1em"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.4"
      strokeLinecap="round"
      strokeLinejoin="round"
      focusable="false"
    >
      <path d="m7.5 8-5 4 5 4m9-8 5 4-5 4m-2-12.5-5 17" />
    </svg>
  );
}

const filters = [
  { id: "all", icon: null },
  { id: "chat", icon: MessageOutlined },
  { id: "image", icon: PictureOutlined },
  { id: "video", icon: VideoCameraOutlined },
  { id: "embedding", icon: EmbeddingOutlined },
  { id: "rerank", icon: OrderedListOutlined },
  { id: "tts", icon: SoundOutlined },
  { id: "asr", icon: AudioOutlined },
  { id: "unknown", icon: QuestionCircleOutlined },
] as const;

export type ModelTaskFilter = (typeof filters)[number]["id"];

export function matchesModelTaskFilter(model: ProviderModel, filter: ModelTaskFilter) {
  const type = getModelTaskType(model.capability);
  if (filter === "all") return true;
  if (filter === "image") return type === "image-generation" || type === "image-edit";
  if (filter === "video") return type === "video-generation";
  return type === filter;
}

export function ModelTaskTabs({ models, value, onChange }: {
  models: readonly ProviderModel[];
  value: ModelTaskFilter;
  onChange: (value: ModelTaskFilter) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="provider-model-type-tabs" role="group" aria-label={t("modelProvider.workspace.modelType")}>
      {filters.map(({ id, icon: Icon }) => (
        <button
          type="button"
          key={id}
          aria-pressed={value === id}
          className={value === id ? "is-active" : undefined}
          onClick={() => onChange(id)}
        >
          {Icon ? <Icon aria-hidden="true" /> : null}
          <span>{t(`modelProvider.taskTab.${id}`)}</span>
          <small>{models.filter((model) => matchesModelTaskFilter(model, id)).length}</small>
        </button>
      ))}
    </div>
  );
}
