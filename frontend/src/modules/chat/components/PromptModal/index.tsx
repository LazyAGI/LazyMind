import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import type { ChangeEvent } from "react";
import {
  Alert,
  Button,
  Empty,
  Form,
  Input,
  Modal,
  Segmented,
  Select,
  Skeleton,
  Tooltip,
  message,
} from "antd";
import {
  AppstoreOutlined,
  BarChartOutlined,
  DeleteOutlined,
  EditOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  FundOutlined,
  PlusOutlined,
  ProfileOutlined,
  SearchOutlined,
  StarFilled,
  StarOutlined,
  TableOutlined,
  UnorderedListOutlined,
} from "@ant-design/icons";
import type {
  PromptCategory,
  PromptItem,
  PromptPatchRequest,
  PromptRequest,
} from "@/api/generated/core-client";
import { PromptServiceApi } from "@/modules/chat/utils/request";
import { useTranslation } from "react-i18next";
import { canManagePrompt, setPromptFavorite } from "./promptLibrary";
import { localizeErrorCode } from "@/components/request";
import "./index.scss";

interface ForwardProps {
  onSelectPrompt: (prompt: string) => void; // 将选中话术写入聊天输入框
}

export interface PromptImperativeProps {
  onOpen: () => void; // 打开话术库
}

interface PromptFormValues {
  display_name: string; // 话术标题
  content: string; // 话术正文
  category: string; // 固定分类编码
}

interface PromptCategoryFormValues {
  name: string; // 用户自定义分类名称
}

type PromptScope = "all" | "recent" | "favorite" | "custom";
type PromptSort = "updated_desc" | "usage_desc" | "name_asc";
type PromptLayout = "grid" | "list";

const CATEGORY_KEYS = [
  "general",
  "document_processing",
  "information_extraction",
  "structured_analysis",
  "report_generation",
  "data_analysis",
  "custom",
] as const;

const CATEGORY_ICONS = {
  general: FolderOpenOutlined,
  document_processing: FileTextOutlined,
  information_extraction: FileSearchOutlined,
  structured_analysis: ProfileOutlined,
  report_generation: BarChartOutlined,
  data_analysis: FundOutlined,
  custom: AppstoreOutlined,
};

function CategoryIcon({ category }: { category?: string }) {
  const Icon =
    CATEGORY_ICONS[category as keyof typeof CATEGORY_ICONS] ?? AppstoreOutlined;
  return <Icon aria-hidden />;
}

function PromptModalComponent(
  { onSelectPrompt }: ForwardProps,
  ref: React.ForwardedRef<PromptImperativeProps>,
) {
  const { t, i18n } = useTranslation();
  const [form] = Form.useForm<PromptFormValues>();
  const [categoryForm] = Form.useForm<PromptCategoryFormValues>();
  const requestSequence = useRef(0);
  const [visible, setVisible] = useState(false);
  const [prompts, setPrompts] = useState<PromptItem[]>([]);
  const [customCategories, setCustomCategories] = useState<PromptCategory[]>([]);
  const [facets, setFacets] = useState<{
    scopes?: Record<string, number>; // 各范围数量
    categories?: Record<string, number>; // 各分类数量
  }>({});
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [searchInput, setSearchInput] = useState("");
  const [keyword, setKeyword] = useState("");
  const [category, setCategory] = useState("");
  const [scope, setScope] = useState<PromptScope>("all");
  const [sort, setSort] = useState<PromptSort>("updated_desc");
  const [layout, setLayout] = useState<PromptLayout>("grid");
  const [formVisible, setFormVisible] = useState(false);
  const [editingPrompt, setEditingPrompt] = useState<PromptItem | null>(null);
  const [saving, setSaving] = useState(false);
  const [categoryFormVisible, setCategoryFormVisible] = useState(false);
  const [categorySaving, setCategorySaving] = useState(false);

  useEffect(() => {
    const timer = window.setTimeout(() => setKeyword(searchInput.trim()), 260);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    if (!visible) return undefined;
    const sequence = ++requestSequence.current;
    const controller = new AbortController();
    setLoading(true);
    setLoadError(false);
    void (async () => {
      try {
        const response = await PromptServiceApi().listPrompts(
          {
            pageSize: 1000,
            keyword: keyword || undefined,
            category: category || undefined,
            scope,
            sort,
            locale: i18n.language,
          },
          { signal: controller.signal },
        );
        if (sequence !== requestSequence.current) return;
        setPrompts(response.data.prompts ?? []);
        setCustomCategories(response.data.custom_categories ?? []);
        setFacets(response.data.facets ?? {});
        setTotal(response.data.total ?? 0);
      } catch {
        if (controller.signal.aborted || sequence !== requestSequence.current) return;
        setLoadError(true);
      } finally {
        if (sequence === requestSequence.current) setLoading(false);
      }
    })();
    return () => {
      controller.abort();
      if (sequence === requestSequence.current) requestSequence.current += 1;
    };
  }, [category, i18n.language, keyword, refreshKey, scope, sort, visible]);

  const onOpen = useCallback(() => {
    setVisible(true);
    setSearchInput("");
    setKeyword("");
    setCategory("");
    setScope("all");
    setSort("updated_desc");
  }, []);

  useImperativeHandle(ref, () => ({ onOpen }), [onOpen]);

  const categoryOptions = useMemo(
    () => [
      ...CATEGORY_KEYS.map((key) => ({
        value: key,
        label: t(`chat.promptCategory.${key}`),
      })),
      ...customCategories.map((category) => ({
        value: category.id ?? "",
        label: category.name ?? "",
      })),
    ],
    [customCategories, t],
  );

  const scopeOptions = useMemo(
    () =>
      (["all", "recent", "favorite", "custom"] as PromptScope[]).map((key) => ({
        value: key,
        label: `${t(`chat.promptScope.${key}`)} (${facets.scopes?.[key] ?? 0})`,
      })),
    [facets.scopes, t],
  );

  function openCreateForm() {
    setEditingPrompt(null);
    form.setFieldsValue({ display_name: "", content: "", category: "custom" });
    setFormVisible(true);
  }

  function openCreateCategoryForm() {
    categoryForm.resetFields();
    setCategoryFormVisible(true);
  }

  async function saveCategory() {
    if (categorySaving) return;
    let values: PromptCategoryFormValues;
    try {
      values = await categoryForm.validateFields();
    } catch {
      return;
    }
    setCategorySaving(true);
    try {
      const response = await PromptServiceApi().createPromptCategory({
        name: values.name.trim(),
      });
      const created = response.data;
      if (created.id) {
        setCustomCategories((items) =>
          [...items.filter((item) => item.id !== created.id), created].sort((left, right) =>
            (left.name ?? "").localeCompare(right.name ?? ""),
          ),
        );
        setCategory(created.id);
      }
      setCategoryFormVisible(false);
      categoryForm.resetFields();
      setRefreshKey((value) => value + 1);
      message.success(t("chat.promptCategoryCreateSuccess"));
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setCategorySaving(false);
    }
  }

  function deleteCategory(promptCategory: PromptCategory) {
    if (!promptCategory.id) return;
    Modal.confirm({
      title: t("chat.promptCategoryDeleteTitle"),
      content: t("chat.promptCategoryDeleteDescription", { name: promptCategory.name }),
      okText: t("common.delete"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      async onOk() {
        try {
          await PromptServiceApi().deletePromptCategory(promptCategory.id!);
          setCustomCategories((items) => items.filter((item) => item.id !== promptCategory.id));
          if (category === promptCategory.id) setCategory("");
          setRefreshKey((value) => value + 1);
          message.success(t("chat.promptCategoryDeleteSuccess"));
        } catch (error) {
          throw error;
        }
      },
    });
  }

  function openEditForm(prompt: PromptItem) {
    if (!canManagePrompt(prompt)) return;
    setEditingPrompt(prompt);
    form.setFieldsValue({
      display_name: prompt.display_name ?? "",
      content: prompt.content ?? "",
      category: prompt.category ?? "custom",
    });
    setFormVisible(true);
  }

  async function savePrompt() {
    if (saving) return;
    let values: PromptFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setSaving(true);
    try {
      if (editingPrompt?.id) {
        const payload: PromptPatchRequest = { ...values };
        await PromptServiceApi().updatePrompt(editingPrompt.id, payload);
      } else {
        const payload: PromptRequest = { ...values };
        await PromptServiceApi().createPrompt(payload);
      }
      message.success(t("chat.promptSaveSuccess"));
      setFormVisible(false);
      form.resetFields();
      setRefreshKey((value) => value + 1);
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setSaving(false);
    }
  }

  function deletePrompt(prompt: PromptItem) {
    if (!prompt.id || !canManagePrompt(prompt)) return;
    Modal.confirm({
      title: t("chat.promptDeleteTitle"),
      content: t("chat.promptDeleteDescription", { name: prompt.display_name }),
      okText: t("common.delete"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      async onOk() {
        try {
          await PromptServiceApi().deletePrompt(prompt.id!);
          message.success(t("chat.deletePromptSuccess"));
          setRefreshKey((value) => value + 1);
        } catch (error) {
          throw error;
        }
      },
    });
  }

  async function toggleFavorite(prompt: PromptItem) {
    if (!prompt.id) return;
    const nextFavorite = !prompt.is_favorite;
    setPrompts((items) => setPromptFavorite(items, prompt.id!, nextFavorite));
    try {
      if (nextFavorite) {
        await PromptServiceApi().favoritePrompt(prompt.id);
      } else {
        await PromptServiceApi().unfavoritePrompt(prompt.id);
      }
      setRefreshKey((value) => value + 1);
    } catch {
      setPrompts((items) => setPromptFavorite(items, prompt.id!, !nextFavorite));
    }
  }

  function usePrompt(prompt: PromptItem) {
    const content = prompt.content?.trim();
    if (!content) return;
    setVisible(false);
    onSelectPrompt(content);
    if (prompt.id) {
      void PromptServiceApi().usePrompt(prompt.id).catch(() => {});
    }
  }

  function formatUpdatedAt(prompt: PromptItem) {
    if (prompt.source === "preset" || !prompt.updated_at) {
      return t("chat.promptBuiltIn");
    }
    const updatedAt = new Date(prompt.updated_at);
    if (Number.isNaN(updatedAt.getTime())) return t("chat.promptUpdatedRecently");
    const elapsedDays = Math.max(
      0,
      Math.floor((Date.now() - updatedAt.getTime()) / 86_400_000),
    );
    if (elapsedDays === 0) return t("chat.promptUpdatedToday");
    if (elapsedDays < 30) {
      return t("chat.promptUpdatedDaysAgo", { count: elapsedDays });
    }
    return updatedAt.toLocaleDateString(i18n.language, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  }

  function renderPromptCard(prompt: PromptItem) {
    const promptID = prompt.id ?? prompt.name ?? prompt.display_name;
    const manageable = canManagePrompt(prompt);
    return (
      <article
        key={promptID}
        className={`prompt-library-card ${layout} ${manageable ? "is-manageable" : ""}`}
      >
        <button
          type="button"
          className="prompt-card-main"
          onClick={() => usePrompt(prompt)}
          aria-label={`${t("chat.usePrompt")} ${prompt.display_name ?? ""}`}
        >
          <span className={`prompt-category-icon category-${prompt.category}`}>
            <CategoryIcon category={prompt.category} />
          </span>
          <span className="prompt-card-copy">
            <span className="prompt-card-title">{prompt.display_name}</span>
            <span className="prompt-card-description">{prompt.content}</span>
          </span>
          <span className="prompt-card-meta">
            <span>{t("chat.promptUsageCount", { count: prompt.usage_count ?? 0 })}</span>
            <span>{formatUpdatedAt(prompt)}</span>
          </span>
        </button>
        <div className="prompt-card-actions">
          <Tooltip
            title={
              prompt.is_favorite
                ? t("chat.promptUnfavorite")
                : t("chat.promptFavorite")
            }
          >
            <button
              type="button"
              className={`prompt-card-action-button ${prompt.is_favorite ? "is-active" : ""}`}
              onClick={() => void toggleFavorite(prompt)}
              aria-label={
                prompt.is_favorite
                  ? t("chat.promptUnfavorite")
                  : t("chat.promptFavorite")
              }
              aria-pressed={Boolean(prompt.is_favorite)}
            >
              {prompt.is_favorite ? <StarFilled /> : <StarOutlined />}
            </button>
          </Tooltip>
          {manageable ? (
            <>
              <Tooltip title={t("common.edit")}>
                <button
                  type="button"
                  className="prompt-card-action-button"
                  onClick={() => openEditForm(prompt)}
                  aria-label={`${t("common.edit")} ${prompt.display_name ?? ""}`}
                >
                  <EditOutlined />
                </button>
              </Tooltip>
              <Tooltip title={t("common.delete")}>
                <button
                  type="button"
                  className="prompt-card-action-button is-danger"
                  onClick={() => deletePrompt(prompt)}
                  aria-label={`${t("common.delete")} ${prompt.display_name ?? ""}`}
                >
                  <DeleteOutlined />
                </button>
              </Tooltip>
            </>
          ) : null}
        </div>
      </article>
    );
  }

  return (
    <>
      <Modal
        title={t("chat.promptTemplateTitle")}
        className="prompt-library-modal"
        width="min(1240px, calc(100vw - 48px))"
        centered
        open={visible}
        onCancel={() => setVisible(false)}
        footer={null}
      >
        <div className="prompt-library-shell">
          <div className="prompt-library-header">
            <Input
              allowClear
              className="prompt-library-search"
              prefix={<SearchOutlined aria-hidden />}
              placeholder={t("chat.searchPromptPlaceholder")}
              aria-label={t("chat.searchPromptPlaceholder")}
              value={searchInput}
              onChange={(event: ChangeEvent<HTMLInputElement>) =>
                setSearchInput(event.target.value)
              }
            />
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreateForm}>
              {t("chat.newTemplate")}
            </Button>
          </div>

          <div className="prompt-library-body">
            <aside className="prompt-library-sidebar" aria-label={t("chat.promptCategoryLabel")}>
              <div className="prompt-category-sidebar-header">
                <span>{t("chat.promptCategoryLabel")}</span>
                <Tooltip title={t("chat.promptCategoryCreateButton")}>
                  <button
                    type="button"
                    className="prompt-category-create-button"
                    onClick={openCreateCategoryForm}
                    aria-label={t("chat.promptCategoryCreateButton")}
                  >
                    <PlusOutlined aria-hidden />
                  </button>
                </Tooltip>
              </div>
              <button
                type="button"
                className={!category ? "is-active" : ""}
                onClick={() => setCategory("")}
              >
                <UnorderedListOutlined aria-hidden />
                <span>{t("chat.promptAllCategories")}</span>
                <strong>{facets.scopes?.all ?? 0}</strong>
              </button>
              {CATEGORY_KEYS.map((key) => (
                <button
                  type="button"
                  key={key}
                  className={category === key ? "is-active" : ""}
                  onClick={() => setCategory(key)}
                >
                  <CategoryIcon category={key} />
                  <span>{t(`chat.promptCategory.${key}`)}</span>
                  <strong>{facets.categories?.[key] ?? 0}</strong>
                </button>
              ))}
              {customCategories.map((promptCategory) => (
                <div
                  className={`prompt-custom-category-row ${category === promptCategory.id ? "is-active" : ""}`}
                  key={promptCategory.id}
                >
                  <button
                    type="button"
                    className="prompt-custom-category-select"
                    onClick={() => setCategory(promptCategory.id ?? "")}
                    aria-pressed={category === promptCategory.id}
                  >
                    <AppstoreOutlined aria-hidden />
                    <span>{promptCategory.name}</span>
                    <strong>{facets.categories?.[promptCategory.id ?? ""] ?? 0}</strong>
                  </button>
                  <Tooltip title={t("common.delete")}>
                    <button
                      type="button"
                      className="prompt-category-delete-button"
                      onClick={() => deleteCategory(promptCategory)}
                      aria-label={`${t("common.delete")} ${promptCategory.name ?? ""}`}
                    >
                      <DeleteOutlined aria-hidden />
                    </button>
                  </Tooltip>
                </div>
              ))}
            </aside>

            <main className="prompt-library-content">
              <div className="prompt-library-toolbar">
                <Segmented
                  className="prompt-scope-segmented"
                  value={scope}
                  options={scopeOptions}
                  onChange={(value: string | number) =>
                    setScope(value as PromptScope)
                  }
                  aria-label={t("chat.promptScopeLabel")}
                />
                <div className="prompt-library-view-controls">
                  <Segmented
                    value={layout}
                    options={[
                      {
                        value: "grid",
                        icon: <TableOutlined aria-label={t("chat.promptGridView")} />,
                      },
                      {
                        value: "list",
                        icon: <UnorderedListOutlined aria-label={t("chat.promptListView")} />,
                      },
                    ]}
                    onChange={(value: string | number) =>
                      setLayout(value as PromptLayout)
                    }
                  />
                  <Select
                    value={sort}
                    aria-label={t("chat.promptSortLabel")}
                    onChange={(value: PromptSort) => setSort(value)}
                    options={[
                      { value: "updated_desc", label: t("chat.promptSortUpdated") },
                      { value: "usage_desc", label: t("chat.promptSortUsage") },
                      { value: "name_asc", label: t("chat.promptSortName") },
                    ]}
                  />
                </div>
              </div>

              <div className="prompt-library-results" aria-live="polite" aria-busy={loading}>
                {loadError ? (
                  <Alert
                    type="error"
                    showIcon
                    message={localizeErrorCode("2000509")}
                    action={
                      <Button size="small" onClick={() => setRefreshKey((value) => value + 1)}>
                        {t("common.retry")}
                      </Button>
                    }
                  />
                ) : null}
                {loading ? (
                  <div className={`prompt-library-grid ${layout}`}>
                    {Array.from({ length: 6 }, (_, index) => (
                      <div className="prompt-library-skeleton" key={index}>
                        <Skeleton active title paragraph={{ rows: 3 }} />
                      </div>
                    ))}
                  </div>
                ) : null}
                {!loading && !loadError && prompts.length === 0 ? (
                  <Empty description={t("chat.noPromptMatched")}>
                    {scope === "custom" || category === "custom" ? (
                      <Button type="primary" icon={<PlusOutlined />} onClick={openCreateForm}>
                        {t("chat.newTemplate")}
                      </Button>
                    ) : null}
                  </Empty>
                ) : null}
                {!loading && !loadError && prompts.length > 0 ? (
                  <div className={`prompt-library-grid ${layout}`}>
                    {prompts.map(renderPromptCard)}
                  </div>
                ) : null}
              </div>
              <div className="prompt-library-total">{t("chat.promptTotal", { count: total })}</div>
            </main>
          </div>
        </div>
      </Modal>

      <Modal
        title={editingPrompt ? t("chat.editPromptTemplate") : t("chat.addPromptTemplate")}
        className="prompt-edit-modal"
        width={540}
        centered
        open={formVisible}
        maskClosable={false}
        confirmLoading={saving}
        okButtonProps={{ disabled: saving }}
        cancelButtonProps={{ disabled: saving }}
        okText={t("common.save")}
        cancelText={t("common.cancel")}
        onCancel={() => {
          if (!saving) setFormVisible(false);
        }}
        onOk={() => void savePrompt()}
      >
        <Form form={form} layout="vertical" requiredMark="optional">
          <Form.Item
            name="display_name"
            label={t("chat.promptTitle")}
            rules={[
              { required: true, whitespace: true, message: t("chat.enterPromptTitle") },
              { max: 100, message: t("chat.promptTitleTooLong") },
            ]}
          >
            <Input placeholder={t("chat.enterPromptTitle")} showCount maxLength={100} />
          </Form.Item>
          <Form.Item
            name="category"
            label={t("chat.promptCategoryLabel")}
            rules={[{ required: true, message: t("chat.promptCategoryRequired") }]}
          >
            <Select options={categoryOptions} />
          </Form.Item>
          <Form.Item
            name="content"
            label={t("chat.promptContent")}
            rules={[
              { required: true, whitespace: true, message: t("chat.enterPromptContent") },
              { max: 800, message: t("chat.promptContentTooLong") },
            ]}
          >
            <Input.TextArea
              placeholder={t("chat.enterPromptContent")}
              rows={8}
              showCount
              maxLength={800}
              autoSize={{ minRows: 7, maxRows: 12 }}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={t("chat.promptCategoryCreateTitle")}
        className="prompt-category-modal"
        width={420}
        centered
        open={categoryFormVisible}
        confirmLoading={categorySaving}
        okButtonProps={{ disabled: categorySaving }}
        cancelButtonProps={{ disabled: categorySaving }}
        okText={t("common.save")}
        cancelText={t("common.cancel")}
        onCancel={() => {
          if (!categorySaving) setCategoryFormVisible(false);
        }}
        onOk={() => void saveCategory()}
      >
        <Form form={categoryForm} layout="vertical" requiredMark="optional">
          <Form.Item
            name="name"
            label={t("chat.promptCategoryName")}
            rules={[
              { required: true, whitespace: true, message: t("chat.promptCategoryNameRequired") },
              { max: 30, message: t("chat.promptCategoryNameTooLong") },
            ]}
          >
            <Input
              placeholder={t("chat.promptCategoryNamePlaceholder")}
              showCount
              maxLength={30}
            />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}

export default forwardRef(PromptModalComponent);
