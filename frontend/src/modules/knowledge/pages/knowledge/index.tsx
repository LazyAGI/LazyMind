import { message, Tag, Row, Col } from "antd";
import { useEffect, useMemo, useState, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useParams, useSearchParams, useNavigate } from "react-router-dom";
import { CopyOutlined } from "@ant-design/icons";
import moment from "moment";
import { Doc, Segment } from "@/api/generated/knowledge-client";

import { TIME_FORMAT } from "@/modules/knowledge/constants/common";
import FileUtils from "@/modules/knowledge/utils/file";
import FileViewer from "@/modules/knowledge/components/FileViewer";
import KnowledgeTabs from "./components/KnowledgeTabs";
import {
  DocumentServiceApi,
  SegmentServiceApi,
  KnowledgeBaseServiceApi,
  normalizeProxyableUrl,
} from "@/modules/knowledge/utils/request";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";
import { DetailPageHeader } from "@/components/ui";
import "./index.scss";

type KnowledgeDetail = Doc & {
  file_url?: string;
  download_file_url?: string;
};

const Detail = () => {
  const { t } = useTranslation();
  const [knowledgeDetail, setKnowledgeDetail] = useState<KnowledgeDetail>();

  const { knowledgeBaseId = "", knowledgeId = "" } = useParams();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [segmentDetail, setSegmentDetail] = useState<Segment>();
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);

  const {
    getDatasetDetail: getKbDetail,
    setCurrentDataset,
    clearDataset,
  } = useDatasetPermissionStore();
  const hasWritePermission = useDatasetPermissionStore((state) =>
    state.hasWritePermission(),
  );

  const group = useMemo(() => {
    return searchParams.get("group_name") || "";
  }, [searchParams]);

  const segmentId = useMemo(() => {
    return searchParams.get("segement_id") || "";
  }, [searchParams]);

  const getDetail = useCallback(() => {
    DocumentServiceApi()
      .documentServiceGetDocument({
        dataset: knowledgeBaseId,
        document: knowledgeId,
      })
      .then((res) => {
        setKnowledgeDetail(res.data);
      });
  }, [knowledgeBaseId, knowledgeId]);

  const getDatasetDetail = useCallback(() => {
    KnowledgeBaseServiceApi()
      .datasetServiceGetDataset({ dataset: knowledgeBaseId })
      .then((res) => {
        setCurrentDataset(res.data);
      });
  }, [knowledgeBaseId, setCurrentDataset]);

  useEffect(() => {
    getDetail();
    getDatasetDetail();

    return () => {
      clearDataset();
    };
  }, [getDetail, getDatasetDetail, clearDataset]);

  useEffect(() => {
    const syncDeveloperActive = () => {
      setDeveloperActive(isDeveloperModeActive());
    };

    const handleDeveloperActiveChange = (event: Event) => {
      const nextActive = (event as CustomEvent<{ active?: boolean }>).detail?.active;
      setDeveloperActive(
        typeof nextActive === "boolean" ? nextActive : isDeveloperModeActive(),
      );
    };

    window.addEventListener("storage", syncDeveloperActive);
    window.addEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperActiveChange);

    return () => {
      window.removeEventListener("storage", syncDeveloperActive);
      window.removeEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperActiveChange);
    };
  }, []);

  const getSegmentDetail = useCallback(() => {
    if (group && segmentId) {
      SegmentServiceApi()
        .segmentServiceGetSegment({
          dataset: knowledgeBaseId,
          document: knowledgeId,
          segment: segmentId,
          group: group,
        })
        .then((res) => {
          setSegmentDetail(res.data);
        });
    }
  }, [group, segmentId, knowledgeBaseId, knowledgeId]);

  useEffect(() => {
    getSegmentDetail();
  }, [group, segmentId, getSegmentDetail]);

  const previewFile = useMemo(() => {
    const filePath = knowledgeDetail?.file_url;
    if (!filePath) {
      return "";
    }

    const fileUrl = `${window.location.origin}/api/core${filePath}`;
    return normalizeProxyableUrl(fileUrl);
  }, [knowledgeDetail?.download_file_url, knowledgeDetail?.file_url]);

  return (
    <div className="knowledge-container !h-full !items-start">
      <DetailPageHeader
        breadcrumbs={[
          { title: t("layout.knowledgeBase"), href: "/lib/knowledge/list" },
          {
            title: getKbDetail()?.display_name || t("knowledge.detail"),
            href: `/lib/knowledge/detail/${getKbDetail()?.dataset_id}`,
          },
          { title: knowledgeDetail?.display_name },
        ]}
        title={knowledgeDetail?.display_name}
        onBack={() => {
          const bool = ["aiwrite", "aireview", "chat"].includes(
            searchParams.get("from") ?? "",
          );
          if (bool) {
            navigate(`/lib/knowledge/detail/${knowledgeBaseId}?from=aiwrite`);
          } else {
            navigate(-1);
          }
        }}
        titleExtra={
          developerActive ? (
            <div>
              <span
                style={{
                  marginRight: "4px",
                  color: "var(--color-text-description)",
                }}
              >
                ID: {knowledgeId}
              </span>
              <CopyOutlined
                style={{ color: "var(--color-text-description)" }}
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(knowledgeId);
                    message.success(t("knowledge.copySuccess"));
                  } catch {
                    message.error(t("knowledge.copyFailedManual"));
                  }
                }}
              />
            </div>
          ) : null
        }
        extraContent={[
          { label: t("knowledge.source"), value: t("knowledge.localFile") },
          {
            label: t("knowledge.createTime"),
            value: moment(knowledgeDetail?.create_time).format(TIME_FORMAT),
          },
          {
            label: t("knowledge.creator"),
            value: knowledgeDetail?.creator || "-",
          },
          {
            label: t("knowledge.originalFile"),
            value: (
              <a
                href={previewFile}
                rel="noreferrer noopener"
                target="_blank"
                title={knowledgeDetail?.display_name}
              >
                {knowledgeDetail?.display_name}
              </a>
            ),
            hidden: !hasWritePermission,
          },
          {
            label: t("knowledge.updateTime"),
            value: moment(knowledgeDetail?.update_time).format(TIME_FORMAT),
          },
          {
            label: t("knowledge.size"),
            value:
              FileUtils.formatFileSize(knowledgeDetail?.document_size) || "-",
          },
          {
            label: t("knowledge.tags"),
            value:
              knowledgeDetail?.tags && knowledgeDetail?.tags.length > 0
                ? knowledgeDetail.tags.map((tag) => (
                    <Tag style={{ marginLeft: "8px" }} key={tag}>
                      {tag}
                    </Tag>
                  ))
                : "-",
          },
        ]}
      />
      <Row gutter={[12, 12]} className="mt-6 min-h-0 w-full flex-1">
        <Col span={15} className="min-h-0">
          <div className="flex h-full min-h-0 flex-col overflow-hidden">
            <FileViewer
              file={previewFile}
              fileName={knowledgeDetail?.display_name || ""}
              segment={segmentDetail}
            />
          </div>
        </Col>
        <Col span={9} className="min-h-0">
          <div style={{ height: '100%', display: 'flex', flexDirection: 'column', overflow: 'hidden', paddingBottom: '4px' }}>
            {knowledgeDetail && (
              <KnowledgeTabs
                knowledgeDetail={knowledgeDetail}
                onGetItemInfo={(data) => {
                  setSegmentDetail(data);
                }}
              />
            )}
          </div>
        </Col>
      </Row>
    </div>
  );
};

export default Detail;
