import { Alert, Input, Modal, Select, Skeleton, Tag } from "antd";
import type { SkillShareRecord, SkillShareStatus } from "../skillApi";
import type { ShareTarget } from "../shared";

interface ShareModalProps {
  t: any;
  shareModalOpen: boolean;
  closeShareModal: () => void;
  handleConfirmShare: () => Promise<void>;
  shareTarget: ShareTarget | null;
  shareDraft: {
    groupIds: string[];
    userIds: string[];
    message: string;
  };
  setShareDraft: any;
  shareLoading: boolean;
  shareGroups: any[];
  shareUsers: any[];
  shareStatusLoading: boolean;
  shareStatusError: string;
  shareStatusRecords: SkillShareRecord[];
  getSkillShareStatusMeta: (
    status: SkillShareStatus,
  ) => { color: string; text: string };
  formatDateTime: (value?: string) => string;
}

export default function ShareModal(props: ShareModalProps) {
  const {
    t,
    shareModalOpen,
    closeShareModal,
    handleConfirmShare,
    shareTarget,
    shareDraft,
    setShareDraft,
    shareLoading,
    shareGroups,
    shareUsers,
    shareStatusLoading,
    shareStatusError,
    shareStatusRecords,
    getSkillShareStatusMeta,
    formatDateTime,
  } = props;

  return (
    <Modal
      open={shareModalOpen}
      title={t("admin.memoryShareDialogTitle")}
      onCancel={closeShareModal}
      onOk={handleConfirmShare}
      okText={t("admin.memoryShareSubmit")}
      cancelText={t("common.cancel")}
      width={720}
    >
      {shareTarget ? (
        <div className="memory-share-modal">
          <div className="memory-share-summary">
            <div className="memory-share-summary-title">
              {shareTarget.item.name}
            </div>
            <div className="memory-share-summary-desc">
              {t("admin.memoryShareSkillHint")}
            </div>
          </div>

          <div className="memory-share-grid">
            <div className="memory-form-field">
              <label>{t("admin.memoryShareGroups")}</label>
              <Select
                mode="multiple"
                allowClear
                showSearch
                optionFilterProp="label"
                placeholder={t("admin.memoryShareGroupsPlaceholder")}
                value={shareDraft.groupIds}
                loading={shareLoading}
                options={shareGroups.map((item) => ({
                  label: item.group_name,
                  value: item.group_id,
                }))}
                onChange={(value) =>
                  setShareDraft((previous: any) => ({ ...previous, groupIds: value }))
                }
              />
            </div>

            <div className="memory-form-field">
              <label>{t("admin.memoryShareUsers")}</label>
              <Select
                mode="multiple"
                allowClear
                showSearch
                optionFilterProp="label"
                placeholder={t("admin.memoryShareUsersPlaceholder")}
                value={shareDraft.userIds}
                loading={shareLoading}
                options={shareUsers.map((item) => ({
                  label: item.display_name
                    ? `${item.display_name} (${item.username})`
                    : item.username,
                  value: item.user_id,
                }))}
                onChange={(value) =>
                  setShareDraft((previous: any) => ({ ...previous, userIds: value }))
                }
              />
            </div>
          </div>

          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memorySkillShareMessage")}</label>
            <Input.TextArea
              rows={4}
              value={shareDraft.message}
              placeholder={t("admin.memorySkillShareMessagePlaceholder")}
              onChange={(event) =>
                setShareDraft((previous: any) => ({
                  ...previous,
                  message: event.target.value,
                }))
              }
            />
          </div>

          <div className="memory-share-selected">
            <div className="memory-share-selected-header">
              <div className="memory-share-selected-title">
                {t("admin.memoryShareCurrentRecipients")}
              </div>
            </div>
            <div className="memory-share-selected-tags">
              {shareDraft.groupIds.map((groupId) => {
                const matchedGroup = shareGroups.find((item) => item.group_id === groupId);
                return matchedGroup ? (
                  <Tag key={groupId} color="blue">
                    {matchedGroup.group_name}
                  </Tag>
                ) : null;
              })}
              {shareDraft.userIds.map((userId) => {
                const matchedUser = shareUsers.find((item) => item.user_id === userId);
                return matchedUser ? (
                  <Tag key={userId} color="green">
                    {matchedUser.display_name || matchedUser.username}
                  </Tag>
                ) : null;
              })}
              {!shareDraft.groupIds.length && !shareDraft.userIds.length ? (
                <span className="memory-share-empty">
                  {t("admin.memoryShareEmptyRecipients")}
                </span>
              ) : null}
            </div>
          </div>

          <div className="memory-share-selected memory-share-status-panel">
              <div className="memory-share-selected-header">
                <div>
                  <div className="memory-share-selected-title">
                    {t("admin.memoryShareSyncedRecipients")}
                  </div>
                  <div className="memory-share-selected-subtitle">
                    {t("admin.memoryShareSyncedRecipientsHint")}
                  </div>
                </div>
              </div>

              {shareStatusLoading ? (
                <Skeleton active title={false} paragraph={{ rows: 3 }} />
              ) : shareStatusError ? (
                <Alert
                  type="error"
                  showIcon
                  message={shareStatusError}
                />
              ) : shareStatusRecords.length ? (
                <div className="memory-share-status-list">
                  {shareStatusRecords.map((record) => {
                    const statusMeta = getSkillShareStatusMeta(record.status);
                    const recipientNames = record.recipients
                      .map((recipient) => recipient.name)
                      .filter(Boolean);
                    const recipientLabel =
                      recipientNames.join(" / ") ||
                      t("admin.memorySkillShareUnknownRecipient");
                    const latestTime = record.decidedAt || record.updatedAt;
                    const timeLabel = latestTime
                      ? `${t("admin.memorySkillShareHandledAt")} ${formatDateTime(latestTime)}`
                      : record.createdAt
                        ? `${t("admin.memorySkillShareSharedAt")} ${formatDateTime(
                            record.createdAt,
                          )}`
                        : "";
                    const metaText =
                      record.status === "failed" && record.errorMessage
                        ? record.errorMessage
                        : timeLabel;

                    return (
                      <div key={record.id} className="memory-share-status-item">
                        <div className="memory-share-status-item-main">
                          <div className="memory-share-status-item-name">
                            {recipientLabel}
                          </div>
                          {metaText ? (
                            <div className="memory-share-status-item-meta">{metaText}</div>
                          ) : null}
                        </div>
                        <Tag color={statusMeta.color}>{statusMeta.text}</Tag>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <span className="memory-share-empty">
                  {t("admin.memoryShareSyncedRecipientsEmpty")}
                </span>
              )}
          </div>
        </div>
      ) : null}
    </Modal>
  );
}
