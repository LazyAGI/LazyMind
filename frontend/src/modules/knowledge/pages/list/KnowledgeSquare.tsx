import {
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type MouseEvent,
  type ReactNode,
} from "react";
import { Button, Input, Modal, Select, Spin } from "antd";
import {
  AuditOutlined,
  BankOutlined,
  BookOutlined,
  BuildOutlined,
  ClockCircleOutlined,
  DatabaseOutlined,
  ExperimentOutlined,
  FileTextOutlined,
  FundOutlined,
  GlobalOutlined,
  MedicineBoxOutlined,
  SearchOutlined,
  ShoppingOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import {
  filterOfficialKnowledgeBases,
  type KnowledgeSquareInstallStatus,
  type KnowledgeSquareType,
  type OfficialKnowledgeBase,
} from "./knowledgeSquareData";

interface KnowledgeSquareProps {
  items: OfficialKnowledgeBase[];
  domains: Record<KnowledgeSquareType, string[]>;
  loading: boolean;
  progressByItem: Record<string, number>;
  onInstall: (item: OfficialKnowledgeBase) => void;
  onUpdate: (item: OfficialKnowledgeBase) => void;
  onOpen: (item: OfficialKnowledgeBase) => void;
  onQuery: (item: OfficialKnowledgeBase) => void;
  onLoadDetail: (item: OfficialKnowledgeBase) => Promise<OfficialKnowledgeBase>;
}

const iconByType: Record<string, ReactNode> = {
  law: <AuditOutlined />,
  finance: <FundOutlined />,
  government: <BankOutlined />,
  medical: <MedicineBoxOutlined />,
  education: <BookOutlined />,
  internet: <GlobalOutlined />,
  people: <TeamOutlined />,
  manufacturing: <BuildOutlined />,
  retail: <ShoppingOutlined />,
  energy: <ThunderboltOutlined />,
  evaluation: <ExperimentOutlined />,
};

function renderIcon(icon: string) {
  return iconByType[icon] || (icon ? <span>{icon}</span> : <DatabaseOutlined />);
}

function formatDate(value: string) {
  return value ? value.slice(0, 10) : "-";
}

export default function KnowledgeSquare({
  items,
  domains,
  loading,
  progressByItem,
  onInstall,
  onUpdate,
  onOpen,
  onQuery,
  onLoadDetail,
}: KnowledgeSquareProps) {
  const { t } = useTranslation();
  const [type, setType] = useState<KnowledgeSquareType>("industry");
  const [domain, setDomain] = useState("");
  const [status, setStatus] = useState<KnowledgeSquareInstallStatus>("all");
  const [keyword, setKeyword] = useState("");
  const [detailItem, setDetailItem] = useState<OfficialKnowledgeBase | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const detailRequestRef = useRef(0);

  const visibleItems = useMemo(
    () =>
      filterOfficialKnowledgeBases({
        items,
        type,
        domain,
        status,
        keyword,
      }),
    [domain, items, keyword, status, type],
  );

  const setActiveType = (nextType: KnowledgeSquareType) => {
    setType(nextType);
    setDomain("");
  };

  const resetFilters = () => {
    setKeyword("");
    setStatus("all");
    setDomain("");
  };

  const openDetail = (item: OfficialKnowledgeBase) => {
    const requestId = ++detailRequestRef.current;
    setDetailItem(item);
    setDetailLoading(true);
    onLoadDetail(item)
      .then((detail) => {
        if (requestId === detailRequestRef.current) setDetailItem(detail);
      })
      .finally(() => {
        if (requestId === detailRequestRef.current) setDetailLoading(false);
      });
  };

  const closeDetail = () => {
    detailRequestRef.current += 1;
    setDetailItem(null);
    setDetailLoading(false);
  };

  return (
    <div className="knowledge-square-view">
      <div
        className="knowledge-square-type-tabs"
        role="tablist"
        aria-label={t("knowledge.squareTypeTabs")}
      >
        <button
          className={type === "industry" ? "is-active" : ""}
          type="button"
          role="tab"
          aria-selected={type === "industry"}
          onClick={() => setActiveType("industry")}
        >
          {t("knowledge.industryKnowledge")}
        </button>
        <button
          className={type === "evaluation" ? "is-active" : ""}
          type="button"
          role="tab"
          aria-selected={type === "evaluation"}
          onClick={() => setActiveType("evaluation")}
        >
          {t("knowledge.evaluationKnowledge")}
        </button>
      </div>

      <div className="knowledge-square-toolbar">
        <Input
          allowClear
          value={keyword}
          prefix={<SearchOutlined />}
          placeholder={t("knowledge.squareSearchPlaceholder")}
          aria-label={t("knowledge.squareSearchPlaceholder")}
          onChange={(event: ChangeEvent<HTMLInputElement>) =>
            setKeyword(event.target.value)
          }
        />
        <Select<KnowledgeSquareInstallStatus>
          value={status}
          aria-label={t("knowledge.installStatus")}
          options={[
            { value: "all", label: t("knowledge.allStatuses") },
            { value: "uninstalled", label: t("knowledge.uninstalled") },
            { value: "installed", label: t("knowledge.installed") },
          ]}
          onChange={setStatus}
        />
        <Button onClick={resetFilters}>{t("common.reset")}</Button>
      </div>

      <div
        className="knowledge-square-domain-tabs"
        aria-label={t("knowledge.domainFilter")}
      >
        {["", ...domains[type]].map((item) => (
          <button
            key={item || "all"}
            className={domain === item ? "is-active" : ""}
            type="button"
            onClick={() => setDomain(item)}
          >
            {item || t("common.all")}
          </button>
        ))}
      </div>

      <Spin spinning={loading}>
        <div className="knowledge-square-grid">
          {visibleItems.map((item) => {
            const active = item.active || progressByItem[item.id] !== undefined;
            const progress = progressByItem[item.id];
            return (
              <article
                key={item.id}
                className={`knowledge-square-card ${item.installed ? "is-installed" : ""}`}
                tabIndex={0}
                role="button"
                aria-label={t("knowledge.viewSquareDetail", { name: item.name })}
                data-icon={item.icon}
                onClick={() => openDetail(item)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    openDetail(item);
                  }
                }}
              >
                <span className="knowledge-square-card-icon" aria-hidden="true">
                  {renderIcon(item.icon)}
                </span>
                <div className="knowledge-square-card-heading">
                  <div className="knowledge-square-card-title-line">
                    <strong>{item.name}</strong>
                    {item.installed ? (
                      <span className="knowledge-square-installed-label">
                        {t("knowledge.installed")}
                      </span>
                    ) : null}
                  </div>
                  <span>{item.domain}</span>
                </div>
                <p>{item.desc}</p>
                <div className="knowledge-square-card-tags">
                  {item.tags.slice(0, 2).map((tag) => (
                    <span key={tag}>{tag}</span>
                  ))}
                </div>
                <div className="knowledge-square-card-meta">
                  <span>
                    <FileTextOutlined />
                    {item.source || t("knowledge.officialKnowledge")}
                  </span>
                  <span>
                    <ClockCircleOutlined />
                    {formatDate(item.updated)}
                  </span>
                </div>
                <div className="knowledge-square-card-actions">
                  <span className={active ? "is-update" : "is-ready"}>
                    <i />
                    {active
                      ? progress === undefined
                        ? t("knowledge.processing")
                        : t("knowledge.taskProgressPercent", {
                            progress,
                          })
                      : item.installed
                        ? t("knowledge.available")
                        : t("knowledge.uninstalled")}
                  </span>
                  <div>
                    {item.onlineAccessUrl ? (
                      <Button
                        size="small"
                        onClick={(event: MouseEvent<HTMLElement>) => {
                          event.stopPropagation();
                          onQuery(item);
                        }}
                      >
                        {t("knowledge.onlineQuery")}
                      </Button>
                    ) : null}
                    <Button
                      size="small"
                      type="primary"
                      loading={active}
                      disabled={active}
                      onClick={(event: MouseEvent<HTMLElement>) => {
                        event.stopPropagation();
                        if (item.installed) onUpdate(item);
                        else onInstall(item);
                      }}
                    >
                      {item.installed
                        ? t("knowledge.checkForUpdates")
                        : t("common.install")}
                    </Button>
                  </div>
                </div>
              </article>
            );
          })}
          {!loading && visibleItems.length === 0 ? (
            <div className="knowledge-square-empty">
              <DatabaseOutlined />
              <strong>{t("knowledge.squareEmptyTitle")}</strong>
              <span>{t("knowledge.squareEmptyDescription")}</span>
            </div>
          ) : null}
        </div>
      </Spin>

      <Modal
        className="knowledge-square-detail-modal"
        width={680}
        open={Boolean(detailItem)}
        title={detailItem?.name}
        centered
        footer={
          detailItem ? (
            <>
              {detailItem.onlineAccessUrl ? (
                <Button onClick={() => onQuery(detailItem)}>
                  {t("knowledge.onlineQuery")}
                </Button>
              ) : null}
              {detailItem.installed ? (
                <Button onClick={() => onOpen(detailItem)}>
                  {t("common.open")}
                </Button>
              ) : null}
              <Button
                type="primary"
                loading={detailItem.active}
                disabled={detailItem.active}
                onClick={() => {
                  if (detailItem.installed) onUpdate(detailItem);
                  else onInstall(detailItem);
                  closeDetail();
                }}
              >
                {detailItem.installed
                  ? t("knowledge.checkForUpdates")
                  : t("common.install")}
              </Button>
            </>
          ) : null
        }
        onCancel={closeDetail}
      >
        <Spin spinning={detailLoading}>
          {detailItem ? (
            <div className="knowledge-square-detail-content">
              <p>{detailItem.desc}</p>
              <dl>
                <div>
                  <dt>{t("knowledge.domainFilter")}</dt>
                  <dd>{detailItem.domain || "-"}</dd>
                </div>
                <div>
                  <dt>{t("knowledge.source")}</dt>
                  <dd>{detailItem.source || "-"}</dd>
                </div>
                <div>
                  <dt>{t("knowledge.updateDate")}</dt>
                  <dd>{formatDate(detailItem.updated)}</dd>
                </div>
                {detailItem.installedAt ? (
                  <div>
                    <dt>{t("knowledge.installedAt")}</dt>
                    <dd>{formatDate(detailItem.installedAt)}</dd>
                  </div>
                ) : null}
              </dl>
              {detailItem.questions.length > 0 ? (
                <div className="knowledge-square-detail-questions">
                  <strong>{t("knowledge.exampleQuestions")}</strong>
                  {detailItem.questions.map((question) => (
                    <span key={question}>{question}</span>
                  ))}
                </div>
              ) : null}
            </div>
          ) : null}
        </Spin>
      </Modal>
    </div>
  );
}
