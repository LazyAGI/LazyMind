import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Empty,
  Modal,
  Popconfirm,
  Segmented,
  Skeleton,
  Space,
  Tag,
} from "antd";
import type { SkillShareStatus, SkillOrganizeTaskRecord } from "../skillApi";
import { listSkillOrganizeTasks } from "../skillApi";
import {
  skillOrganizeErrorText,
  skillOrganizeModeLabel,
  skillOrganizeStatusLabel,
} from "../skillOrganizeCopy";
import type { SkillShareCenterTab } from "../shared";

interface SkillShareCenterModalProps {
  t: any;
  hideShareTabs?: boolean;
  skillShareCenterOpen: boolean;
  closeSkillShareCenter: () => void;
  skillShareCenterTab: SkillShareCenterTab;
  setSkillShareCenterTab: (value: SkillShareCenterTab) => void;
  incomingPendingCount: number;
  outgoingSkillShares: any[];
  skillShareCenterLoading: boolean;
  refreshSkillShareCenter: (options?: { showErrorToast?: boolean }) => Promise<void>;
  skillShareCenterError: string;
  currentSkillShareList: any[];
  skillShareActionState: Record<string, string | undefined>;
  getSkillShareStatusMeta: (status: SkillShareStatus) => { color: string; text: string };
  formatDateTime: (value?: string) => string;
  previewSkillShare: (share: any) => Promise<void>;
  rejectIncomingSkillShare: (share: any) => Promise<void>;
  acceptIncomingSkillShare: (share: any) => Promise<void>;
  isSkillShareActionable: (status: SkillShareStatus) => boolean;
}

export default function SkillShareCenterModal(props: SkillShareCenterModalProps) {
  const {
    t,
    hideShareTabs = false,
    skillShareCenterOpen,
    closeSkillShareCenter,
    skillShareCenterTab,
    setSkillShareCenterTab,
    incomingPendingCount,
    outgoingSkillShares,
    skillShareCenterLoading,
    refreshSkillShareCenter,
    skillShareCenterError,
    currentSkillShareList,
    skillShareActionState,
    getSkillShareStatusMeta,
    formatDateTime,
    previewSkillShare,
    rejectIncomingSkillShare,
    acceptIncomingSkillShare,
    isSkillShareActionable,
  } = props;
  const activeTab: SkillShareCenterTab = hideShareTabs ? "organize" : skillShareCenterTab;
  const [organizeTasks, setOrganizeTasks] = useState<SkillOrganizeTaskRecord[]>([]);
  const [organizeLoading, setOrganizeLoading] = useState(false);
  const [organizeError, setOrganizeError] = useState("");

  const loadOrganizeHistory = async () => {
    setOrganizeLoading(true);
    setOrganizeError("");
    try {
      setOrganizeTasks(await listSkillOrganizeTasks(50));
    } catch (error) {
      setOrganizeTasks([]);
      setOrganizeError(skillOrganizeErrorText("", "", t));
      console.error("Load skill organize history failed:", error);
    } finally {
      setOrganizeLoading(false);
    }
  };

  useEffect(() => {
    if (!skillShareCenterOpen || activeTab !== "organize") {
      return;
    }
    void loadOrganizeHistory();
  }, [activeTab, skillShareCenterOpen]);

  const skillLabel = (value: string) => {
    const text = value.trim();
    const parts = text.split("/").filter(Boolean);
    return parts[parts.length - 1] || text;
  };

  return (
    <Modal
      open={skillShareCenterOpen}
      title={t("admin.memorySkillShareCenterTitle")}
      onCancel={closeSkillShareCenter}
      width={960}
      footer={[
        <Button key="close" onClick={closeSkillShareCenter}>
          {t("common.close")}
        </Button>,
      ]}
    >
      <div className="memory-skill-share-center">
        <div className="memory-skill-share-toolbar">
          <Segmented<SkillShareCenterTab>
            value={activeTab}
            onChange={(value) => setSkillShareCenterTab(value)}
            options={hideShareTabs ? [
              {
                label: t("admin.memorySkillOrganizeHistory"),
                value: "organize",
              },
            ] : [
              {
                label: t("admin.memorySkillShareCenterIncoming", {
                  count: incomingPendingCount,
                }),
                value: "incoming",
              },
              {
                label: t("admin.memorySkillShareCenterOutgoing", {
                  count: outgoingSkillShares.length,
                }),
                value: "outgoing",
              },
              {
                label: t("admin.memorySkillOrganizeHistory"),
                value: "organize",
              },
            ]}
          />
          <Button
            loading={activeTab === "organize" ? organizeLoading : skillShareCenterLoading}
            onClick={() => {
              if (activeTab === "organize") {
                void loadOrganizeHistory();
                return;
              }
              void refreshSkillShareCenter({ showErrorToast: true });
            }}
          >
            {t("admin.memorySkillShareRefresh")}
          </Button>
        </div>

        {activeTab === "organize" ? (
          <>
            {organizeError ? <Alert type="error" showIcon message={organizeError} /> : null}
            {organizeLoading && !organizeTasks.length ? (
              <Skeleton active paragraph={{ rows: 6 }} />
            ) : organizeTasks.length ? (
              <div className="memory-skill-share-list">
                {organizeTasks.map((item) => {
                  const mode = skillOrganizeModeLabel(item.mode, t);
                  const when = item.task?.finishedAt || item.task?.createdAt;
                  const errorText = item.status === "failed"
                    ? skillOrganizeErrorText(item.errorCode, item.error, t)
                    : "";
                  return (
                    <div key={item.requestId || item.task?.id} className="memory-skill-share-card">
                      <div className="memory-skill-share-card-head">
                        <div className="memory-skill-share-card-title">
                          <strong>{skillOrganizeStatusLabel(item.status, t)}</strong>
                          {mode ? <span>{mode}</span> : null}
                        </div>
                        <Tag color={item.status === "failed" ? "error" : item.status === "completed" || item.status === "done" ? "success" : "processing"}>
                          {skillOrganizeStatusLabel(item.status, t)}
                        </Tag>
                      </div>
                      <div className="memory-skill-share-card-body">
                        {item.skills.length ? (
                          <div className="memory-skill-share-card-line">
                            <strong>{t("admin.memorySkillOrganizeHistorySkills")}</strong>
                            <span>{item.skills.map(skillLabel).join("、")}</span>
                          </div>
                        ) : null}
                        {when ? (
                          <div className="memory-skill-share-card-line">
                            <strong>{t("admin.memorySkillOrganizeHistoryTime")}</strong>
                            <span>{formatDateTime(when)}</span>
                          </div>
                        ) : null}
                        {errorText ? (
                          <div className="memory-skill-share-card-line">
                            <strong>{t("admin.memorySkillOrganizeFailed")}</strong>
                            <span>{errorText}</span>
                          </div>
                        ) : null}
                      </div>
                    </div>
                  );
                })}
              </div>
            ) : (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t("admin.memorySkillOrganizeHistoryEmpty")}
              />
            )}
          </>
        ) : skillShareCenterError ? (
          <Alert
            type="error"
            showIcon
            message={skillShareCenterError}
            action={
              <Button
                size="small"
                onClick={() => void refreshSkillShareCenter({ showErrorToast: true })}
              >
                {t("common.retry")}
              </Button>
            }
          />
        ) : null}

        {activeTab !== "organize" && (skillShareCenterLoading && !currentSkillShareList.length ? (
          <Skeleton active paragraph={{ rows: 6 }} />
        ) : currentSkillShareList.length ? (
          <div className="memory-skill-share-list">
            {currentSkillShareList.map((share) => {
              const statusMeta = getSkillShareStatusMeta(share.status);
              const shareAction = skillShareActionState[share.id];
              const latestTime = share.decidedAt || share.updatedAt;

              return (
                <div key={share.id} className="memory-skill-share-card">
                  <div className="memory-skill-share-card-head">
                    <div className="memory-skill-share-card-title">
                      <strong>
                        {share.skillName || t("admin.memorySkillShareUnknownSkill")}
                      </strong>
                      <span>
                        {share.skillDescription || t("admin.memorySkillShareNoDescription")}
                      </span>
                    </div>
                    <Space size={8} wrap>
                      <Tag color={statusMeta.color}>{statusMeta.text}</Tag>
                    </Space>
                  </div>

                  <div className="memory-skill-share-card-body">
                    {skillShareCenterTab === "incoming" ? (
                      <div className="memory-skill-share-card-line">
                        <strong>{t("admin.memorySkillShareSender")}</strong>
                        <span>
                          {share.sender?.name || t("admin.memorySkillShareUnknownSender")}
                        </span>
                      </div>
                    ) : null}
                    <div className="memory-skill-share-card-line">
                      <strong>{t("admin.memorySkillShareRecipients")}</strong>
                      <div className="memory-tag-group">
                        {share.recipients.length ? (
                          share.recipients.map((recipient: any, index: number) => (
                            <Tag
                              key={`${share.id}-${recipient.type}-${recipient.id}-${index}`}
                            >
                              {recipient.name}
                            </Tag>
                          ))
                        ) : (
                          <span className="memory-content-preview">
                            {t("admin.memorySkillShareUnknownRecipient")}
                          </span>
                        )}
                      </div>
                    </div>
                    <div className="memory-skill-share-card-line">
                      <strong>{t("admin.memorySkillShareMessage")}</strong>
                      <span>
                        {share.message || t("admin.memorySkillShareNoMessage")}
                      </span>
                    </div>
                    <div className="memory-skill-share-card-line">
                      <strong>{t("admin.memorySkillShareSharedAt")}</strong>
                      <span>{formatDateTime(share.createdAt)}</span>
                    </div>
                    {latestTime ? (
                      <div className="memory-skill-share-card-line">
                        <strong>{t("admin.memorySkillShareHandledAt")}</strong>
                        <span>{formatDateTime(latestTime)}</span>
                      </div>
                    ) : null}
                  </div>

                  <div className="memory-skill-share-card-actions">
                    <Button
                      size="small"
                      loading={shareAction === "preview"}
                      disabled={Boolean(shareAction) && shareAction !== "preview"}
                      onClick={() => void previewSkillShare(share)}
                    >
                      {t("admin.memorySkillSharePreview")}
                    </Button>
                    {skillShareCenterTab === "incoming" ? (
                      <>
                        <Popconfirm
                          title={t("admin.memorySkillShareRejectConfirmTitle")}
                          okText={t("admin.memorySkillShareReject")}
                          cancelText={t("common.cancel")}
                          okButtonProps={{ danger: true }}
                          onConfirm={() => void rejectIncomingSkillShare(share)}
                        >
                          <Button
                            size="small"
                            danger
                            loading={shareAction === "reject"}
                            disabled={Boolean(shareAction)}
                          >
                            {t("admin.memorySkillShareReject")}
                          </Button>
                        </Popconfirm>
                        <Button
                          type="primary"
                          size="small"
                          loading={shareAction === "accept"}
                          disabled={
                            !isSkillShareActionable(share.status) || Boolean(shareAction)
                          }
                          onClick={() => void acceptIncomingSkillShare(share)}
                        >
                          {t("admin.memorySkillShareAccept")}
                        </Button>
                      </>
                    ) : null}
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={
              activeTab === "incoming"
                ? t("admin.memorySkillShareEmptyIncoming")
                : t("admin.memorySkillShareEmptyOutgoing")
            }
          />
        ))}
      </div>
    </Modal>
  );
}
