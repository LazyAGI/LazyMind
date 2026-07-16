import { Button, message, Tag, Tooltip, Row, Col } from "antd";
import { useEffect, useMemo, useRef, useState, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useParams, useSearchParams, useNavigate } from "react-router-dom";
import { CopyOutlined, FileImageOutlined } from "@ant-design/icons";
import moment from "moment";
import { Doc } from "@/api/generated/core-client";
import { Segment } from "@/api/generated/knowledge-client";

import type { Dataset as KnowledgeDataset } from "@/api/generated/knowledge-client";
import { TIME_FORMAT } from "@/modules/knowledge/constants/common";
import FileUtils from "@/modules/knowledge/utils/file";
import FileViewer, {
  type FileViewerRef,
} from "@/modules/knowledge/components/FileViewer";
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
import { localizeErrorCode } from "@/components/request";
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
  const fileViewerRef = useRef<FileViewerRef>(null);
  const [canExportImagePdf, setCanExportImagePdf] = useState(false);
  const [exportingImagePdf, setExportingImagePdf] = useState(false);

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
        setCurrentDataset(res.data as unknown as KnowledgeDataset);
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
      const nextActive = (event as CustomEvent<{ active?: boolean }>).detail
        ?.active;
      setDeveloperActive(
        typeof nextActive === "boolean" ? nextActive : isDeveloperModeActive(),
      );
    };

    window.addEventListener("storage", syncDeveloperActive);
    window.addEventListener(
      DEVELOPER_ACTIVE_EVENT,
      handleDeveloperActiveChange,
    );

    return () => {
      window.removeEventListener("storage", syncDeveloperActive);
      window.removeEventListener(
        DEVELOPER_ACTIVE_EVENT,
        handleDeveloperActiveChange,
      );
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

  const handleExportImagePdf = useCallback(async () => {
    if (!canExportImagePdf || exportingImagePdf) {
      return;
    }
    setExportingImagePdf(true);
    try {
      await fileViewerRef.current?.exportImagePdf();
      message.success("已导出图片 PDF");
    } catch {
      message.error(localizeErrorCode("2000509"));
    } finally {
      setExportingImagePdf(false);
    }
  }, [canExportImagePdf, exportingImagePdf]);

  const pageTitle = useMemo(() => {
    const displayName = knowledgeDetail?.display_name;
    if (!displayName) {
      return displayName;
    }
    if (!canExportImagePdf) {
      return displayName;
    }
    return (
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 8,
          minWidth: 0,
          maxWidth: "100%",
        }}
      >
        <Tooltip title={displayName}>
          <span className="detail-title-text">{displayName}</span>
        </Tooltip>
        <Tooltip title="导出成图片pdf">
          <Button
            type="text"
            size="small"
            icon={<FileImageOutlined />}
            loading={exportingImagePdf}
            onClick={handleExportImagePdf}
            style={{ flexShrink: 0 }}
          />
        </Tooltip>
      </span>
    );
  }, [
    canExportImagePdf,
    exportingImagePdf,
    handleExportImagePdf,
    knowledgeDetail?.display_name,
  ]);

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
        title={pageTitle}
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
          <FileViewer
            ref={fileViewerRef}
            file={previewFile}
            fileName={knowledgeDetail?.display_name || ""}
            segment={segmentDetail}
            onExportReadyChange={setCanExportImagePdf}
          />
        </Col>
        <Col span={9} className="min-h-0">
          <div
            style={{
              height: "100%",
              display: "flex",
              flexDirection: "column",
              overflow: "hidden",
              paddingBottom: "4px",
            }}
          >
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
