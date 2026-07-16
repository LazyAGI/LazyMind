import {
  Modal as AntdModal,
  Table as AntdTable,
  Button as AntdButton,
  Space as AntdSpace,
  message as AntdMessage,
  Input as AntdInput,
  Tag as AntdTag,
  Tooltip as AntdTooltip,
} from "antd";
import type { TableColumnsType } from "antd";
import { useState, useEffect, useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { createGroupApi, createUserApi } from "@/modules/signin/utils/request";
import type { GroupItem, GroupUserItem, UserItem } from "@/api/generated/auth-client";
import {
  SearchOutlined,
  RightOutlined,
  LeftOutlined,
  UsergroupAddOutlined,
} from "@ant-design/icons";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import { useStyles } from "@/components/ui/useStyles";

const manageMembersModalCss = `
.manage-members-modal .ant-modal {
  max-width: calc(100vw - 32px);
}

.manage-members-modal__transfer {
  display: flex;
  align-items: stretch;
  gap: 12px;
  min-height: 450px;
}

.manage-members-modal__panel {
  flex: 1 1 0;
  min-width: 0;
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.manage-members-modal__panel-header {
  padding: 12px;
  border-bottom: 1px solid #f0f0f0;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.manage-members-modal__search {
  padding: 8px;
}

.manage-members-modal__table {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.manage-members-modal__actions {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 8px;
  flex: 0 0 auto;
}

.manage-members-modal__break-text {
  overflow-wrap: anywhere;
  word-break: break-word;
}

.manage-members-modal__ellipsis-text {
  display: block;
  width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.manage-members-modal__empty {
  padding: 40px 0;
  color: #bfbfbf;
}

.manage-members-modal .ant-table-wrapper {
  height: 100%;
}

.manage-members-modal .ant-table-container table {
  table-layout: fixed;
}

.manage-members-modal .ant-pagination {
  margin-inline: 8px;
}

@media (max-width: 960px) {
  .manage-members-modal__transfer {
    flex-direction: column;
    min-height: auto;
  }

  .manage-members-modal__actions {
    flex-direction: row;
    justify-content: center;
  }
}
`;

interface ManageMembersModalProps {
  visible: boolean;
  group: GroupItem | null;
  isAdmin: boolean;
  onCancel: () => void;
  onSuccess?: () => void;
}

interface GroupMemberListItem extends GroupUserItem {
  email?: string;
}

const ManageMembersModal = ({
  visible,
  group,
  isAdmin,
  onCancel,
  onSuccess,
}: ManageMembersModalProps) => {
  const { t } = useTranslation();
  useStyles("manage-members-modal-styles", manageMembersModalCss);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [allUsers, setAllUsers] = useState<UserItem[]>([]);
  const [currentMembers, setCurrentMembers] = useState<GroupMemberListItem[]>([]);
  const [leftSelectedKeys, setLeftSelectedKeys] = useState<string[]>([]);
  const [rightSelectedKeys, setRightSelectedKeys] = useState<string[]>([]);
  const [pendingAddUsers, setPendingAddUsers] = useState<UserItem[]>([]);
  const [leftSearch, setLeftSearch] = useState("");
  const [rightSearch, setRightSearch] = useState("");
  const isUserInactive = (status?: string) =>
    status?.toLowerCase() === "inactive";

  const fetchAllUsers = useCallback(async () => {
    if (!isAdmin) return;
    try {
      const userApi = createUserApi();
      const pageSize = 200;
      let page = 1;
      let total = 0;
      const users: UserItem[] = [];

      do {
        const res = await userApi.listUsersApiAuthserviceUserGet({
          page,
          pageSize,
        });
        const resData = res.data as any;
        const data = resData.data || resData || {};
        const currentUsers = data.users || [];
        users.push(...currentUsers);
        total = Number(data.total || users.length);
        page += 1;
      } while (users.length < total);

      setAllUsers(users);
    } catch (error) {
      console.error("Failed to fetch users:", error);
    }
  }, [isAdmin]);

  const fetchMembers = useCallback(async () => {
    if (!group) return;
    setLoading(true);
    try {
      const groupApi = createGroupApi();
      const res =
        await groupApi.listGroupUsersApiAuthserviceGroupGroupIdUserGet({
          groupId: group.group_id,
        });
      const resData = res.data as any;
      const members =
        (resData.users || resData.data?.users || []) as GroupMemberListItem[];
      setCurrentMembers(members);
    } catch (error) {
      console.error("Failed to fetch members:", error);
    } finally {
      setLoading(false);
    }
  }, [group, t]);

  useEffect(() => {
    if (!visible || !group) return;

    fetchMembers();
    if (isAdmin) {
      fetchAllUsers();
    }
    setPendingAddUsers([]);
    setLeftSelectedKeys([]);
    setRightSelectedKeys([]);
    setLeftSearch("");
    setRightSearch("");
  }, [visible, group, isAdmin, fetchAllUsers, fetchMembers]);

  const leftDataSource = useMemo(() => {
    return allUsers.filter((user) => {
      const isMember = currentMembers.some((m) => m.user_id === user.user_id);
      const isPending = pendingAddUsers.some((p) => p.user_id === user.user_id);
      const matchesSearch = user.username
        .toLowerCase()
        .includes(leftSearch.toLowerCase());
      return !isMember && !isPending && matchesSearch;
    });
  }, [allUsers, currentMembers, pendingAddUsers, leftSearch]);

  const rightDataSource = useMemo(() => {
    return pendingAddUsers.filter((user) =>
      user.username.toLowerCase().includes(rightSearch.toLowerCase()),
    );
  }, [pendingAddUsers, rightSearch]);

  const moveToRight = () => {
    const usersToMove = leftDataSource.filter(
      (u) => leftSelectedKeys.includes(u.user_id) && !isUserInactive(u.status),
    );
    setPendingAddUsers((prev) => [...prev, ...usersToMove]);
    setLeftSelectedKeys([]);
  };

  const moveToLeft = () => {
    setPendingAddUsers((prev) =>
      prev.filter((u) => !rightSelectedKeys.includes(u.user_id)),
    );
    setRightSelectedKeys([]);
  };

  const handleConfirmAdd = async () => {
    if (!group) return;
    if (pendingAddUsers.length === 0) {
      AntdMessage.warning(t("admin.selectUsersToAdd"));
      return;
    }

    setSaving(true);
    try {
      const groupApi = createGroupApi();
      await groupApi.addGroupUsersApiAuthserviceGroupGroupIdUserPost({
        groupId: group.group_id,
        groupAddUsersBody: { user_ids: pendingAddUsers.map((u) => u.user_id) },
      });
      AntdMessage.success(t("admin.addMembersSuccess"));
      setPendingAddUsers([]);
      onSuccess?.();
    } catch (error: any) {
      console.error("Add members failed:", error);
      if (!error?.response && !error?.request) {
        AntdMessage.error(getLocalizedErrorMessage(error));
      }
    } finally {
      setSaving(false);
    }
  };

  const userColumns: TableColumnsType<UserItem> = [
    {
      title: t("admin.username"),
      dataIndex: "username",
      key: "username",
      width: 220,
      render: (value: string) => (
        <AntdTooltip title={value || "-"}>
          <span className="manage-members-modal__ellipsis-text">
            {value || "-"}
          </span>
        </AntdTooltip>
      ),
    },
    {
      title: t("admin.email"),
      dataIndex: "email",
      key: "email",
      width: 240,
      render: (value?: string) => (
        <div className="manage-members-modal__break-text">{value || "-"}</div>
      ),
    },
    {
      title: t("admin.role"),
      dataIndex: "role_name",
      key: "role_name",
      width: 120,
      render: (roleName?: string) => (
        <AntdTag
          color={roleName?.toLowerCase().includes("admin") ? "orange" : "blue"}
        >
          {roleName || "-"}
        </AntdTag>
      ),
    },
  ];

  return (
    <AntdModal
      title={
        <AntdSpace>
          <UsergroupAddOutlined />
          <span>{t("admin.manageMembersTitle", { groupName: group?.group_name })}</span>
        </AntdSpace>
      }
      open={visible}
      onCancel={onCancel}
      footer={[
        <AntdButton key="cancel" onClick={onCancel}>
          {t("common.cancel")}
        </AntdButton>,
        <AntdButton
          key="submit"
          type="primary"
          loading={saving}
          onClick={handleConfirmAdd}
        >
          {t("admin.confirmAdd")}
        </AntdButton>,
      ]}
      width={1080}
      destroyOnHidden
      className="manage-members-modal"
      styles={{
        body: {
          padding: "12px 24px",
          maxHeight: "calc(100vh - 180px)",
          overflowY: "auto",
        },
      }}
    >
      <div style={{ marginBottom: "16px", color: "#666" }}>
        {t("admin.selectUsersForGroup")}
        <span style={{ color: "#1890ff", fontWeight: "bold" }}>
          {group?.group_name}
        </span>
      </div>

      <div className="manage-members-modal__transfer">
        <div className="manage-members-modal__panel">
          <div className="manage-members-modal__panel-header">
            <span style={{ fontWeight: "bold" }}>
              {t("admin.itemsCount", { count: leftDataSource.length })}
            </span>
            <span style={{ color: "#999" }}>{t("admin.available")}</span>
          </div>
          <div className="manage-members-modal__search">
            <AntdInput
              placeholder={t("admin.searchAvailableUsers")}
              prefix={<SearchOutlined style={{ color: "#bfbfbf" }} />}
              value={leftSearch}
              onChange={(e) => setLeftSearch(e.target.value)}
              allowClear
            />
          </div>
          <div className="manage-members-modal__table">
            <AntdTable
              size="small"
              rowSelection={{
                selectedRowKeys: leftSelectedKeys,
                onChange: (keys) => setLeftSelectedKeys(keys as string[]),
                getCheckboxProps: (record) => ({
                  disabled: isUserInactive(record.status),
                }),
              }}
              dataSource={leftDataSource}
              columns={userColumns}
              rowKey="user_id"
              loading={loading}
              tableLayout="fixed"
              scroll={{ x: 620 }}
              pagination={getLocalizedTablePagination({
                size: "small",
                pageSize: 10,
                showSizeChanger: false,
              }, t)}
            />
          </div>
        </div>

        <div className="manage-members-modal__actions">
          <AntdButton
            icon={<RightOutlined />}
            onClick={moveToRight}
            disabled={leftSelectedKeys.length === 0}
            type={leftSelectedKeys.length > 0 ? "primary" : "default"}
          />
          <AntdButton
            icon={<LeftOutlined />}
            onClick={moveToLeft}
            disabled={rightSelectedKeys.length === 0}
            type={rightSelectedKeys.length > 0 ? "primary" : "default"}
          />
        </div>

        <div className="manage-members-modal__panel">
          <div className="manage-members-modal__panel-header">
            <span style={{ fontWeight: "bold" }}>
              {t("admin.itemsCount", { count: rightDataSource.length })}
            </span>
            <span style={{ color: "#999" }}>{t("admin.selected")}</span>
          </div>
          <div className="manage-members-modal__search">
            <AntdInput
              placeholder={t("admin.searchSelectedUsers")}
              prefix={<SearchOutlined style={{ color: "#bfbfbf" }} />}
              value={rightSearch}
              onChange={(e) => setRightSearch(e.target.value)}
              allowClear
            />
          </div>
          <div className="manage-members-modal__table">
            <AntdTable
              size="small"
              rowSelection={{
                selectedRowKeys: rightSelectedKeys,
                onChange: (keys) => setRightSelectedKeys(keys as string[]),
                getCheckboxProps: (record) => ({
                  disabled: isUserInactive(record.status),
                }),
              }}
              dataSource={rightDataSource}
              columns={userColumns.slice(0, 1)}
              rowKey="user_id"
              pagination={false}
              tableLayout="fixed"
              scroll={{ x: 320 }}
              locale={{
                emptyText: (
                  <div className="manage-members-modal__empty">
                    {t("admin.noSelectedUsers")}
                  </div>
                ),
              }}
            />
          </div>
        </div>
      </div>
    </AntdModal>
  );
};

export default ManageMembersModal;
