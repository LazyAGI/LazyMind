import { Empty, Tabs, TabsProps } from "antd";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";

import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";
import {
  Doc,
  ParserConfigTypeEnum,
  Segment,
} from "@/api/generated/knowledge-client";
import type { ParserConfig } from "@/api/generated/core-client";

import SegmentTab from "../SegmentTab";
import SummaryTab from "../SummaryTab";
import QaTab from "../QaTab";
import Rendering from "@/modules/knowledge/components/Rendering";
import "./index.scss";

const TAB_KEYS = {
  summary: "2",
  document: "3:",
  qa: "4",
  imageCaption: "5",
} as const;

const LEGACY_SPLIT_GROUPS = ["lazyllm_root", "block", "line"];

const KnowledgeTabs = (props: {
  knowledgeDetail: Doc;
  onGetItemInfo?: (data: Segment) => void;
}) => {
  const { knowledgeDetail, onGetItemInfo } = props;
  const { t } = useTranslation();

  const [activeKey, setActiveKey] = useState("");
  const [parsers, setParsers] = useState<ParserConfig[]>([]);
  const [tabs, setTabs] = useState<TabsProps["items"]>([]);
  const [searchParams] = useSearchParams();
  const [loading, setLoading] = useState(false);

  const group = useMemo(() => {
    return searchParams.get("group_name") || "";
  }, [searchParams]);

  useEffect(() => {
    setLoading(true);
    KnowledgeBaseServiceApi()
      .datasetServiceGetDataset({ dataset: knowledgeDetail.dataset_id || "" })
      .then((res) => {
        const result = res.data.parsers || [];
        setParsers(result);
        const currentTabs = generateTabs(result);
        setTabs(currentTabs);
        setActiveKey(
          getInitialActiveKey(result, searchParams.get("group_name")) ||
            (currentTabs.length > 0 ? String(currentTabs[0].key) : ""),
        );
      })
      .finally(() => {
        setLoading(false);
      });
  }, [knowledgeDetail]);

  function getSplitTypeLabel(splitType: string, splitCount = 1) {
    if (splitCount <= 1) {
      return t("knowledge.segmentDocument");
    }
    if (splitType === "block") {
      return t("knowledge.segmentSplitBlock");
    }
    if (splitType === "line") {
      return t("knowledge.segmentSplitLine");
    }
    return splitType;
  }

  function generateTabs(configs: ParserConfig[]) {
    if (!configs || configs.length < 1) {
      return [];
    }
    const initTabs: TabsProps["items"] = [];
    configs.forEach((parser) => {
      switch (parser.type) {
        case ParserConfigTypeEnum.ParseTypeSplit:
          if (
            initTabs.some((tab) =>
              String(tab.key).startsWith(TAB_KEYS.document),
            )
          ) {
            break;
          }
          const splitNames = configs
            .filter(
              (config) =>
                config.type === ParserConfigTypeEnum.ParseTypeSplit,
            )
            .map((config) => config.name);
          splitNames.forEach((splitName) => {
            initTabs.push({
              label: getSplitTypeLabel(splitName || "", splitNames.length),
              children: (
                <SegmentTab
                  detail={knowledgeDetail}
                  type={splitName || ""}
                  names={[splitName || ""]}
                  editable={true}
                  onGetItemInfo={onGetItemInfo}
                />
              ),
              key: `${TAB_KEYS.document}${splitName || ""}`,
              closable: false,
            });
          });
          break;
        case ParserConfigTypeEnum.ParseTypeSummary:
          initTabs.push({
            label: t("knowledge.segmentSummary"),
            children: (
              <SummaryTab
                detail={knowledgeDetail}
                type={
                  group === parser.name ? group : parser.name || "summary"
                }
                onGetItemInfo={onGetItemInfo}
              />
            ),
            key: TAB_KEYS.summary,
            closable: false,
          });
          break;
        case ParserConfigTypeEnum.ParseTypeQa:
          initTabs.push({
            label: t("knowledge.segmentQa"),
            children: (
              <QaTab
                detail={knowledgeDetail}
                type={group === parser.name ? group : parser.name || "qa"}
              />
            ),
            key: TAB_KEYS.qa,
            closable: false,
          });
          break;
        case ParserConfigTypeEnum.ParseTypeImageCaption:
          initTabs.push({
            label: t("knowledge.imageCaption"),
            children: (
              <SegmentTab
                detail={knowledgeDetail}
                names={[parser.name as string]}
                type={group === parser.name ? group : parser.name || "hybrid"}
                editable={false}
              />
            ),
            key: TAB_KEYS.imageCaption,
            closable: false,
          });
          break;
      }
    });
    return initTabs;
  }

  function getInitialActiveKey(configs: ParserConfig[], groupName?: string | null) {
    if (!groupName) {
      return "";
    }

    const parser = configs.find((config) => config.name === groupName);
    if (
      parser?.type === ParserConfigTypeEnum.ParseTypeSplit ||
      isSplitGroup(groupName)
    ) {
      const splitParserNames = configs
        .filter((config) => config.type === ParserConfigTypeEnum.ParseTypeSplit)
        .map((config) => config.name)
        .filter((name): name is string => !!name);

      if (groupName && splitParserNames.includes(groupName)) {
        return `${TAB_KEYS.document}${groupName}`;
      }

      if (splitParserNames.length > 0) {
        return `${TAB_KEYS.document}${splitParserNames[0]}`;
      }

      return "";
    }
    if (parser?.type === ParserConfigTypeEnum.ParseTypeSummary) {
      return TAB_KEYS.summary;
    }
    if (parser?.type === ParserConfigTypeEnum.ParseTypeQa) {
      return TAB_KEYS.qa;
    }
    if (parser?.type === ParserConfigTypeEnum.ParseTypeImageCaption) {
      return TAB_KEYS.imageCaption;
    }

    return "";
  }

  function isSplitGroup(groupName?: string, splitNames: string[] = []) {
    return !!groupName && (
      LEGACY_SPLIT_GROUPS.includes(groupName) || splitNames.includes(groupName)
    );
  }

  function onChange(newActiveKey: string) {
    setActiveKey(newActiveKey);
  }

  return loading ? (
    <Rendering text={t("common.loading")} />
  ) : parsers.length < 1 ? (
    <Empty description={t("knowledge.noContent")} style={{ marginTop: 80 }} />
  ) : (
    <Tabs
      type="editable-card"
      className="card-container !h-full"
      hideAdd
      onChange={onChange}
      activeKey={activeKey}
      items={tabs}
    />
  );
};

export default KnowledgeTabs;
