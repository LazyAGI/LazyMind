import { useEffect, useState } from "react";
import {
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Steps,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  CheckCircleOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  QuestionCircleOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  checkDatabaseConnection,
  createDatabaseConnection,
  deleteDatabaseConnection,
  listDatabaseConnections,
  updateDatabaseConnection,
  type DatabaseConnectionItem,
  type DatabaseConnectionPayload,
} from "../api";

const { Paragraph, Text } = Typography;

type FormValues = DatabaseConnectionPayload & {
  options_text?: string;
};

function parseOptions(value?: string): Record<string, string> {
  const text = `${value || ""}`.trim();
  if (!text) {
    return {};
  }
  const parsed = JSON.parse(text) as unknown;
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
    throw new Error("连接参数必须是 JSON 对象");
  }
  return Object.fromEntries(
    Object.entries(parsed).map(([key, item]) => [key, item == null ? "" : String(item)]),
  );
}

function connectionToForm(record: DatabaseConnectionItem): FormValues {
  return {
    display_name: record.display_name,
    description: record.description,
    db_type: record.db_type,
    host: record.host,
    port: record.port,
    database_name: record.database_name,
    username: record.username,
    password: "",
    options: record.options || {},
    options_text: Object.keys(record.options || {}).length > 0
      ? JSON.stringify(record.options, null, 2)
      : "",
  };
}

export default function DatabaseConnectionsPage() {
  const [form] = Form.useForm<FormValues>();
  const [items, setItems] = useState<DatabaseConnectionItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [checkingId, setCheckingId] = useState<string>("");
  const [modalOpen, setModalOpen] = useState(false);
  const [guideOpen, setGuideOpen] = useState(false);
  const [editing, setEditing] = useState<DatabaseConnectionItem | null>(null);

  const refresh = async () => {
    setLoading(true);
    try {
      const result = await listDatabaseConnections();
      setItems(result.connections || []);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, "加载外部数据库连接失败"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ db_type: "postgresql", port: 5432 });
    setModalOpen(true);
  };

  const openEdit = (record: DatabaseConnectionItem) => {
    setEditing(record);
    form.setFieldsValue(connectionToForm(record));
    setModalOpen(true);
  };

  const handleSubmit = async () => {
    const values = await form.validateFields();
    const payload: DatabaseConnectionPayload = {
      display_name: values.display_name.trim(),
      description: values.description?.trim(),
      db_type: values.db_type,
      host: values.host.trim(),
      port: values.port,
      database_name: values.database_name.trim(),
      username: values.username.trim(),
      password: values.password,
      options: parseOptions(values.options_text),
    };
    if (editing && !payload.password) {
      delete payload.password;
    }
    setSaving(true);
    try {
      if (editing) {
        await updateDatabaseConnection(editing.id, payload);
      } else {
        await createDatabaseConnection(payload);
      }
      message.success(editing ? "连接已更新" : "连接已创建");
      setModalOpen(false);
      await refresh();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, "保存外部数据库连接失败"));
    } finally {
      setSaving(false);
    }
  };

  const handleCheck = async (record: DatabaseConnectionItem) => {
    setCheckingId(record.id);
    try {
      const result = await checkDatabaseConnection(record.id);
      if (result.success) {
        message.success(`连接成功，发现 ${result.table_count} 张表`);
      } else {
        message.error(result.message || "连接失败");
      }
      await refresh();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, "测试连接失败"));
    } finally {
      setCheckingId("");
    }
  };

  const handleDelete = (record: DatabaseConnectionItem) => {
    Modal.confirm({
      title: "删除外部数据库连接",
      content: `确认删除 ${record.display_name}？`,
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await deleteDatabaseConnection(record.id);
        message.success("连接已删除");
        await refresh();
      },
    });
  };

  const columns: ColumnsType<DatabaseConnectionItem> = [
    {
      title: "名称",
      dataIndex: "display_name",
      render: (value, record) => (
        <Space direction="vertical" size={0}>
          <Text strong>{value}</Text>
          <Text type="secondary">{record.description || record.database_name}</Text>
        </Space>
      ),
    },
    {
      title: "类型",
      dataIndex: "db_type",
      width: 130,
      render: (value) => <Tag color={value === "mysql" ? "blue" : "geekblue"}>{value}</Tag>,
    },
    {
      title: "地址",
      width: 260,
      render: (_, record) => `${record.host}:${record.port}/${record.database_name}`,
    },
    {
      title: "账号",
      dataIndex: "username",
      width: 180,
    },
    {
      title: "状态",
      dataIndex: "is_verified",
      width: 150,
      render: (verified, record) => verified ? (
        <Tag color="success" icon={<CheckCircleOutlined />}>已验证</Tag>
      ) : (
        <Tag color={record.last_check_error ? "error" : "default"}>未验证</Tag>
      ),
    },
    {
      title: "操作",
      width: 280,
      render: (_, record) => (
        <Space>
          <Button
            icon={<SyncOutlined />}
            loading={checkingId === record.id}
            onClick={() => void handleCheck(record)}
          >
            测试
          </Button>
          <Button icon={<EditOutlined />} onClick={() => openEdit(record)}>编辑</Button>
          <Button danger icon={<DeleteOutlined />} onClick={() => handleDelete(record)}>删除</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="admin-page data-source-page">
      <div className="admin-page-toolbar data-source-page-toolbar">
        <div className="admin-page-toolbar-left data-source-page-toolbar-left">
          <div>
            <h2 className="admin-page-title">外部数据库</h2>
            <Paragraph className="data-source-page-subtitle">
              配置用于聊天只读查询的 MySQL 和 PostgreSQL 数据库连接。
            </Paragraph>
          </div>
        </div>
        <Space>
          <Button icon={<QuestionCircleOutlined />} onClick={() => setGuideOpen(true)}>接入教程</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新增连接</Button>
        </Space>
      </div>

      <Table<DatabaseConnectionItem>
        className="admin-page-table"
        rowKey="id"
        columns={columns}
        dataSource={items}
        loading={loading}
        pagination={false}
        locale={{ emptyText: <Space><DatabaseOutlined />暂无外部数据库连接</Space> }}
      />

      <Modal
        title={editing ? "编辑外部数据库连接" : "新增外部数据库连接"}
        open={modalOpen}
        okText="保存"
        cancelText="取消"
        confirmLoading={saving}
        destroyOnHidden
        onOk={() => void handleSubmit()}
        onCancel={() => setModalOpen(false)}
      >
        <Form<FormValues> form={form} layout="vertical">
          <Form.Item name="display_name" label="名称" rules={[{ required: true, message: "请输入名称" }]}>
            <Input />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input />
          </Form.Item>
          <Space.Compact block>
            <Form.Item name="db_type" label="类型" rules={[{ required: true }]} style={{ width: "40%" }}>
              <Select
                options={[
                  { label: "PostgreSQL", value: "postgresql" },
                  { label: "MySQL", value: "mysql" },
                ]}
                onChange={(value) => form.setFieldValue("port", value === "mysql" ? 3306 : 5432)}
              />
            </Form.Item>
            <Form.Item name="port" label="端口" rules={[{ required: true }]} style={{ width: "60%" }}>
              <InputNumber min={1} max={65535} style={{ width: "100%" }} />
            </Form.Item>
          </Space.Compact>
          <Form.Item name="host" label="主机" rules={[{ required: true, message: "请输入主机" }]}>
            <Input placeholder="db.example.com" />
          </Form.Item>
          <Form.Item name="database_name" label="数据库" rules={[{ required: true, message: "请输入数据库名" }]}>
            <Input />
          </Form.Item>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: "请输入用户名" }]}>
            <Input />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={editing ? [] : [{ required: true, message: "请输入密码" }]}
          >
            <Input.Password placeholder={editing ? "留空则不修改" : undefined} />
          </Form.Item>
          <Form.Item
            name="options_text"
            label="连接参数 JSON"
            rules={[
              {
                validator: async (_, value) => {
                  try {
                    parseOptions(value);
                  } catch (error) {
                    return Promise.reject(error instanceof Error ? error : new Error("连接参数 JSON 格式错误"));
                  }
                  return Promise.resolve();
                },
              },
            ]}
          >
            <Input.TextArea rows={3} placeholder='{"sslmode":"require"}' />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="外部数据库接入教程"
        open={guideOpen}
        footer={<Button type="primary" onClick={() => setGuideOpen(false)}>知道了</Button>}
        onCancel={() => setGuideOpen(false)}
      >
        <Steps
          direction="vertical"
          current={-1}
          items={[
            {
              title: "准备只读账号",
              description: "在 MySQL 或 PostgreSQL 中创建只读用户，并只授予需要查询的库表 SELECT 权限。",
            },
            {
              title: "开放网络访问",
              description: "在云数据库白名单或安全组中放通 LazyMind 部署环境到数据库端口的访问。",
            },
            {
              title: "填写连接信息",
              description: "点击新增连接，填写主机、端口、数据库名、用户名、密码；PostgreSQL 如需 SSL，可在连接参数中填写 {\"sslmode\":\"require\"}。",
            },
            {
              title: "测试并保存",
              description: "保存后点击测试，状态变为已验证即可在聊天中使用。测试只会连接数据库并读取表结构。",
            },
            {
              title: "在聊天中提问",
              description: "直接询问已连接数据库中的数据，例如“查询订单库本月订单量”。Agent 会读取 schema、生成只读 SQL 并返回结果。",
            },
          ]}
        />
      </Modal>
    </div>
  );
}
