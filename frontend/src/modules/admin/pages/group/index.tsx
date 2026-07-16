import { useState, useEffect, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { Table, Button, Space, Popconfirm, message, Input, Tooltip, Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  PlusOutlined,
  DeleteOutlined,
  EditOutlined,
  UsergroupAddOutlined,
} from "@ant-design/icons";
import CreateGroupModal from "./components/CreateGroupModal";
import ManageMembersModal from "./components/ManageMembersModal";
import { createGroupApi } from "@/modules/signin/utils/request";
import { AgentAppsAuth } from "@/components/auth";
import type { GroupItem } from "@/api/generated/auth-client";
import { getLocalizedTablePagination } from "@/components/ui/pagination";

const { Paragraph } = Typography;
const NAME_COLUMN_WIDTH = 220;

const GroupManagement = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [isModalVisible, setIsModalVisible] = useState(false);
  const [isMemberModalVisible, setIsMemberModalVisible] = useState(false);
  const [loading, setLoading] = useState(false);
  const [groups, setGroups] = useState<GroupItem[]>([]);
  const [pagination, setPagination] = useState({
    current: 1,
    pageSize: 20,
    total: 0,
  });
  const [editingGroup, setEditingGroup] = useState<GroupItem | null>(null);
  const [selectedGroupForMembers, setSelectedGroupForMembers] =
    useState<GroupItem | null>(null);
  const [searchTerm, setSearchTerm] = useState("");

  const userInfo = AgentAppsAuth.getUserInfo();
  const isAdmin = (role?: string) => {
    const normalizedRole = (role || "").trim().toLowerCase();
    return (
      normalizedRole === "admin" ||
      normalizedRole === "system-admin" ||
      normalizedRole === "system_admin" ||
      normalizedRole.endsWith(".admin")
    );
  };
  const isUserAdmin = isAdmin(userInfo?.role);

  const fetchGroups = useCallback(async (page = 1, pageSize = 20, search = "") => {
    setLoading(true);
    try {
      const api = createGroupApi();
      const res = await api.listGroupsApiAuthserviceGroupGet({
        page,
        pageSize,
        search: search || undefined,
      });
      const resData = res.data as any;
      const data = resData.data || resData;

      setGroups(data.groups || []);
      setPagination({
        current: Number(data.page || page),
        pageSize: Number(data.page_size || pageSize),
        total: Number(data.total || 0),
      });
    } catch (error) {
      console.error("Failed to fetch groups:", error);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchGroups(pagination.current, pagination.pageSize, searchTerm);
  }, [fetchGroups]);

  const handleSearch = (value: string) => {
    setSearchTerm(value);
    fetchGroups(1, pagination.pageSize, value);
  };

  const handleDelete = async (groupId: string) => {
    try {
      const api = createGroupApi();
      await api.deleteGroupApiAuthserviceGroupGroupIdDelete({ groupId });
      message.success(t("admin.deleteSuccess"));
      fetchGroups(pagination.current, pagination.pageSize, searchTerm);
    } catch {}
  };

  const handleEdit = (group: GroupItem) => {
    setEditingGroup(group);
    setIsModalVisible(true);
  };

  const handleViewGroupDetail = (group: GroupItem) => {
    navigate(`/admin/groups/${group.group_id}`);
  };

  const handleAddMembers = (group: GroupItem) => {
    setSelectedGroupForMembers(group);
    setIsMemberModalVisible(true);
  };

  const renderEllipsisText = (text?: string, emptyText = "-") => {
    if (!text) {
      return emptyText;
    }

    return (
      <Paragraph
        style={{ marginBottom: 0, overflowWrap: "anywhere" }}
        ellipsis={{ rows: 2, tooltip: text }}
      >
        {text}
      </Paragraph>
    );
  };

  const columns = [
    {
      title: t("admin.groupName"),
      dataIndex: "group_name",
      key: "group_name",
      width: NAME_COLUMN_WIDTH,
      ellipsis: true,
      render: (text: string, record: GroupItem) => (
        isUserAdmin ? (
          <Tooltip title={text}>
            <Button
              type="link"
              style={{ padding: 0, width: "100%", display: "block", textAlign: "left" }}
              onClick={() => handleViewGroupDetail(record)}
            >
              <span
                style={{
                  display: "block",
                  width: "100%",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {text}
              </span>
            </Button>
          </Tooltip>
        ) : (
          <Tooltip title={text}>
            <span
              style={{
                display: "inline-block",
                width: "100%",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {text}
            </span>
          </Tooltip>
        )
      ),
    },
    {
      title: t("admin.description"),
      dataIndex: "remark",
      key: "remark",
      width: 360,
      render: (remark: string) => renderEllipsisText(remark),
    },
    {
      title: t("admin.actions"),
      key: "action",
      width: 200,
      render: (_: any, record: GroupItem) => (
        <Space size={4} wrap>
          {isUserAdmin && (
            <>
              <Button
                type="link"
                size="small"
                icon={<UsergroupAddOutlined />}
                onClick={() => handleAddMembers(record)}
              >
                {t("admin.addMembers")}
              </Button>
              <Button
                type="link"
                size="small"
                icon={<EditOutlined />}
                onClick={() => handleEdit(record)}
              >
                {t("common.edit")}
              </Button>
              <Popconfirm
                title={t("admin.deleteGroupConfirm")}
                onConfirm={() => handleDelete(record.group_id)}
                okText={t("common.confirm")}
                cancelText={t("common.cancel")}
              >
                <Button
                  type="link"
                  size="small"
                  danger
                  icon={<DeleteOutlined />}
                >
                  {t("common.delete")}
                </Button>
              </Popconfirm>
            </>
          )}
        </Space>
      ),
    },
  ];

  const handleCreateSuccess = () => {
    setIsModalVisible(false);
    setEditingGroup(null);
    fetchGroups(pagination.current, pagination.pageSize, searchTerm);
  };

  const handleTableChange = (newPagination: any) => {
    fetchGroups(newPagination.current, newPagination.pageSize, searchTerm);
  };

  return (
    <div className="admin-page">
      <div className="admin-page-toolbar">
        <div className="admin-page-toolbar-left">
          <h2 className="admin-page-title">{t("admin.groupManagement")}</h2>
          <Input.Search
            placeholder={t("admin.searchGroupName")}
            allowClear
            onSearch={handleSearch}
            className="admin-page-search"
          />
        </div>
        {isUserAdmin && (
          <Button
            type="primary"
            icon={<PlusOutlined />}
            className="admin-page-primary-button"
            onClick={() => {
              setEditingGroup(null);
              setIsModalVisible(true);
            }}
          >
            {t("admin.newGroup")}
          </Button>
        )}
      </div>

      <Table
        className="admin-page-table"
        columns={columns}
        dataSource={groups}
        rowKey="group_id"
        loading={loading}
        tableLayout="fixed"
        scroll={{ x: 980 }}
        pagination={getLocalizedTablePagination({
          ...pagination,
          showSizeChanger: true,
          showTotal: (total) => t("common.totalItems", { total }),
        }, t)}
        onChange={handleTableChange}
      />

      <CreateGroupModal
        visible={isModalVisible}
        editingGroup={editingGroup}
        onCancel={() => {
          setIsModalVisible(false);
          setEditingGroup(null);
        }}
        onSuccess={handleCreateSuccess}
      />

      <ManageMembersModal
        visible={isMemberModalVisible}
        group={selectedGroupForMembers}
        isAdmin={isUserAdmin}
        onCancel={() => {
          setIsMemberModalVisible(false);
          setSelectedGroupForMembers(null);
        }}
        onSuccess={() => {
          setIsMemberModalVisible(false);
          setSelectedGroupForMembers(null);
        }}
      />
    </div>
  );
};

export default GroupManagement;
