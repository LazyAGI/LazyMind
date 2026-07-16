import { useState, useEffect, useCallback, useMemo, type CSSProperties } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { Card, Table, Button, Space, Input, Tag, Popconfirm, message, Typography, Row, Col } from "antd";
import { useTranslation } from "react-i18next";
import { 
  PlusOutlined, 
  SearchOutlined, 
  EditOutlined,
  CopyOutlined,
} from "@ant-design/icons";
import { createGroupApi } from "@/modules/signin/utils/request";
import type { GroupDetailResponse, GroupUserItem } from "@/api/generated/auth-client";
import { AgentAppsAuth } from "@/components/auth";
import DetailPageHeader from "@/components/ui/DetailPageHeader";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import ManageMembersModal from "./components/ManageMembersModal";
import CreateGroupModal from "./components/CreateGroupModal";

const { Text } = Typography;

const breakTextStyle: CSSProperties = {
  overflowWrap: "anywhere",
  wordBreak: "break-word",
};

const MEMBER_TABLE_SCROLL_Y = 360;

const GroupDetail = () => {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const [group, setGroup] = useState<GroupDetailResponse | null>(null);
  const [members, setMembers] = useState<GroupUserItem[]>([]);
  const [memberLoading, setMemberLoading] = useState(false);
  const [memberSearch, setMemberSearch] = useState("");
  const [isEditModalVisible, setIsEditModalVisible] = useState(false);
  const [isAddMemberModalVisible, setIsAddMemberModalVisible] = useState(false);

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

  useEffect(() => {
    if (!isUserAdmin) {
      navigate("/admin/groups", { replace: true });
    }
  }, [isUserAdmin, navigate]);

  const fetchGroupDetail = useCallback(async () => {
    if (!id || !isUserAdmin) return;
    setLoading(true);
    try {
      const api = createGroupApi();
      const res = await api.getGroupApiAuthserviceGroupGroupIdGet({ groupId: id });
      const resData = res.data as any;
      const data = resData.data || resData;
      setGroup(data);
    } catch (error) {
      console.error("Failed to fetch group detail:", error);
    } finally {
      setLoading(false);
    }
  }, [id, isUserAdmin]);

  const fetchMembers = useCallback(async () => {
    if (!id || !isUserAdmin) return;
    setMemberLoading(true);
    try {
      const api = createGroupApi();
      const res = await api.listGroupUsersApiAuthserviceGroupGroupIdUserGet({ groupId: id });
      const resData = res.data as any;
      const memberList = resData.users || resData.data?.users || [];
      setMembers(memberList);
    } catch (error) {
      console.error("Failed to fetch group members:", error);
    } finally {
      setMemberLoading(false);
    }
  }, [id, isUserAdmin]);

  useEffect(() => {
    if (!isUserAdmin) return;
    fetchGroupDetail();
    fetchMembers();
  }, [fetchGroupDetail, fetchMembers, isUserAdmin]);

  const handleCopy = (text: string) => {
    navigator.clipboard.writeText(text);
    message.success(t("admin.copiedToClipboard"));
  };

  const handleRemoveMember = async (userId: string) => {
    if (!id) return;
    try {
      const api = createGroupApi();
      await api.removeGroupUsersApiAuthserviceGroupGroupIdUserRemovePost({
        groupId: id,
        groupRemoveUsersBody: { user_ids: [userId] }
      });
      message.success(t("admin.removeMemberSuccess"));
      fetchMembers();
    } catch (error) {
      console.error("Failed to remove member:", error);
    }
  };

  const filteredMembers = useMemo(() => {
    return members.filter(m => 
      m.username.toLowerCase().includes(memberSearch.toLowerCase())
    );
  }, [members, memberSearch]);

  const columns = [
    {
      title: t("admin.username"),
      dataIndex: "username",
      key: "username",
      width: 220,
    },
    {
      title: t("admin.remark"),
      dataIndex: "remark",
      key: "remark",
      width: 260,
      render: (text: string) => (
        <Text style={breakTextStyle}>
          {text || "-"}
        </Text>
      ),
    },
    {
      title: t("admin.role"),
      dataIndex: "role",
      key: "role",
      width: 140,
      render: (role: string) => (
        <Tag color={role === "admin" ? "orange" : "blue"}>
          {role === "admin" ? t("admin.groupAdmin") : t("admin.member")}
        </Tag>
      ),
    },
    {
      title: t("admin.joinedAt"),
      dataIndex: "created_at",
      key: "created_at",
      width: 220,
      render: (text: string) => text || "-",
    },
    {
      title: t("admin.actions"),
      key: "action",
      width: 180,
      render: (_: any, record: GroupUserItem) => (
        <Space size="middle">
          {}
          <Popconfirm
            title={t("admin.removeMemberFromGroupConfirm")}
            onConfirm={() => handleRemoveMember(record.user_id)}
            okText={t("common.confirm")}
            cancelText={t("common.cancel")}
          >
            <Button type="link" danger size="small">{t("admin.removeMember")}</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  if (!isUserAdmin) {
    return null;
  }

  return (
    <div style={{ padding: "24px" }}>
      <DetailPageHeader
        breadcrumbs={[
          {
            title: <span style={{ cursor: "pointer" }} onClick={() => navigate("/admin/groups")}>{t("admin.groupManagement")}</span>
          },
          { title: t("admin.groupDetail") }
        ]}
        title={group?.group_name || t("admin.groupDetail")}
        onBack={() => navigate("/admin/groups")}
      />

      <Card 
        loading={loading}
        title={t("admin.basicInfo")}
        extra={isUserAdmin && <Button icon={<EditOutlined />} onClick={() => setIsEditModalVisible(true)}>{t("common.edit")}</Button>}
        style={{ marginTop: "24px" }}
      >
        <Row gutter={[24, 16]}>
          <Col span={12}>
            <Space direction="vertical" size={4} style={{ width: "100%" }}>
              <Text type="secondary">{t("admin.groupName")}</Text>
              <Space>
                <Text strong style={breakTextStyle}>{group?.group_name}</Text>
                <Button type="text" size="small" icon={<CopyOutlined />} onClick={() => handleCopy(group?.group_name || "")} />
              </Space>
            </Space>
          </Col>
          <Col span={12}>
            <Space direction="vertical" size={4} style={{ width: "100%" }}>
              <Text type="secondary">{t("admin.groupId")}</Text>
              <Space>
                <Text style={breakTextStyle}>{group?.group_id}</Text>
                <Button type="text" size="small" icon={<CopyOutlined />} onClick={() => handleCopy(group?.group_id || "")} />
              </Space>
            </Space>
          </Col>
          <Col span={12}>
            <Space direction="vertical" size={4} style={{ width: "100%" }}>
              <Text type="secondary">{t("admin.remark")}</Text>
              <Text style={breakTextStyle}>{group?.remark || "-"}</Text>
            </Space>
          </Col>
          <Col span={12}>
            <Space direction="vertical" size={4} style={{ width: "100%" }}>
              <Text type="secondary">{t("admin.memberCount")}</Text>
              <Text>{members.length}</Text>
            </Space>
          </Col>
        </Row>
      </Card>

      <Card title={t("admin.memberManagement")} style={{ marginTop: "24px" }}>
        <div style={{ marginBottom: "16px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <Input
            placeholder={t("admin.searchUsername")}
            prefix={<SearchOutlined />}
            style={{ width: 250 }}
            value={memberSearch}
            onChange={e => setMemberSearch(e.target.value)}
            allowClear
          />
          {isUserAdmin && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setIsAddMemberModalVisible(true)}>
              {t("admin.addMembers")}
            </Button>
          )}
        </div>
        <Table
          className="admin-page-table"
          columns={columns}
          dataSource={filteredMembers}
          rowKey="user_id"
          loading={memberLoading}
          tableLayout="fixed"
          scroll={{ x: 1020, y: MEMBER_TABLE_SCROLL_Y }}
          pagination={getLocalizedTablePagination(
            { showSizeChanger: true, showTotal: (total) => t("common.totalItems", { total }) },
            t,
          )}
        />
      </Card>

      <CreateGroupModal
        visible={isEditModalVisible}
        editingGroup={group as any}
        onCancel={() => setIsEditModalVisible(false)}
        onSuccess={() => {
          setIsEditModalVisible(false);
          fetchGroupDetail();
        }}
      />

      <ManageMembersModal
        visible={isAddMemberModalVisible}
        group={group as any}
        isAdmin={isUserAdmin}
        onCancel={() => setIsAddMemberModalVisible(false)}
        onSuccess={() => {
          setIsAddMemberModalVisible(false);
          fetchMembers();
        }}
      />
    </div>
  );
};

export default GroupDetail;
