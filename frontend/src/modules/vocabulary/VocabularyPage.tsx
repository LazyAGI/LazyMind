import {
  BookOutlined,
  CheckCircleFilled,
  CloseOutlined,
  CloudSyncOutlined,
  MoreOutlined,
  PlusOutlined,
  SettingOutlined,
  TrophyOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Descriptions,
  Drawer,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Progress,
  Radio,
  Select,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from "antd";
import type { ChangeEvent, CSSProperties, MouseEvent, ReactNode } from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { isVocabularyEnabled } from "@/runtime/mode";
import {
  addVocabularyWord,
  completeVocabularyReviewSession,
  createAnkiDeck,
  createWordbook,
  deleteAnkiDeck,
  deleteVocabulary,
  deleteWordbook,
  getActiveVocabularyReviewSession,
  getAnkiStatus,
  getVocabularyProvider,
  getVocabularyStats,
  listAnkiDecks,
  listReviewLogs,
  listVocabulary,
  listWordbooks,
  masterVocabulary,
  resetVocabulary,
  resetVocabularyBatch,
  resumeVocabulary,
  reviewLogsExportUrl,
  saveVocabularyProvider,
  startVocabularyReviewSession,
  submitVocabularySessionReview,
  updateVocabularyWord,
  type AnkiDeck,
  type AnkiProviderStatus,
  type ReviewLog,
  type ReviewQuestion,
  type ReviewSessionReport,
  type ReviewStats,
  type VocabularyListItem,
  type VocabularyProviderSetting,
  type Wordbook,
} from "./api";
import "./VocabularyPage.scss";

const labels: Record<string, string> = {
  new: "新词",
  learning: "学习中",
  review: "复习中",
  relearning: "重学",
  mastered: "已学会",
  suspended: "已暂停",
};
const ratings: Record<number, string> = {
  1: "错误",
  2: "困难",
  3: "良好",
  4: "简单",
};
const interval = (value?: string) => {
  if (!value) return "";
  const ms = new Date(value).getTime() - Date.now();
  if (ms < 3600000) return `${Math.max(1, Math.round(ms / 60000))} 分钟`;
  if (ms < 86400000) return `${Math.round(ms / 3600000)} 小时`;
  return `${Math.round(ms / 86400000)} 天`;
};
const logIntervals = (log: ReviewLog) => {
  const data = log.fsrs_log || {};
  if ("interval_before_days" in data)
    return [data.interval_before_days || 0, data.interval_after_days || 0];
  return ["ScheduledDays" in data ? data.ScheduledDays || 0 : 0, undefined];
};
const Panel = ({
  children,
  title,
  onClick,
  style,
}: {
  children: ReactNode;
  title?: string;
  onClick?: () => void;
  style?: CSSProperties;
}) => (
  <section
    onClick={onClick}
    style={{
      padding: 20,
      border: "1px solid #e5eaf2",
      borderRadius: 10,
      background: "#fff",
      ...style,
    }}
  >
    {title ? <Typography.Title level={4}>{title}</Typography.Title> : null}
    {children}
  </section>
);

export default function VocabularyPage() {
  const { t } = useTranslation();
  const desktop = isVocabularyEnabled();
  const [tab, setTab] = useState("words");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [newWordOpen, setNewWordOpen] = useState(false);
  const [provider, setProvider] = useState<"local" | "anki">("local");
  const [providerSetting, setProviderSetting] =
    useState<VocabularyProviderSetting>({
      selected_provider: "local",
      anki_endpoint: "http://127.0.0.1:8765",
      anki_deck_name: "LazyMind Vocabulary",
    });
  const [items, setItems] = useState<VocabularyListItem[]>([]);
  const [anki, setAnki] = useState<AnkiProviderStatus | null>(null);
  const [stats, setStats] = useState<ReviewStats | null>(null);
  const [books, setBooks] = useState<Wordbook[]>([]);
  const [ankiDecks, setAnkiDecks] = useState<AnkiDeck[]>([]);
  const [logs, setLogs] = useState<ReviewLog[]>([]);
  const [search, setSearch] = useState("");
  const [state, setState] = useState<string>();
  const [wordbookId, setWordbookId] = useState<string>();
  const [logRange, setLogRange] = useState("today");
  const [question, setQuestion] = useState<ReviewQuestion | null>(null);
  const [revealed, setRevealed] = useState(false);
  const [detail, setDetail] = useState<VocabularyListItem | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [reviewSessionId, setReviewSessionId] = useState("");
  const [reviewQueue, setReviewQueue] = useState<ReviewQuestion[]>([]);
  const [reviewReport, setReviewReport] = useState<ReviewSessionReport | null>(
    null,
  );
  const [hasActiveReview, setHasActiveReview] = useState(false);
  const [objectiveAnswer, setObjectiveAnswer] = useState("");
  const [form] = Form.useForm();
  const [newWordForm] = Form.useForm();
  const load = useCallback(async () => {
    const setting = await getVocabularyProvider();
    const active = setting.selected_provider;
    const [nextBooks, nextDecks] = await Promise.all([
      desktop ? listWordbooks() : Promise.resolve([]),
      active === "anki" ? listAnkiDecks().catch(() => []) : Promise.resolve([]),
    ]);
    let selected = wordbookId || setting.local_default_wordbook_id || "";
    if (active === "local" && !nextBooks.some((book) => book.id === selected))
      selected =
        nextBooks.find((book) => book.name === "默认生词本")?.id ||
        nextBooks[0]?.id ||
        "";
    const [words, status, nextStats, nextLogs, activeReview] =
      await Promise.all([
        listVocabulary(active, {
          search,
          state: state || "",
          wordbook_id: active === "local" ? selected : "",
        }),
        getAnkiStatus().catch(() => null),
        getVocabularyStats(active).catch(() => null),
        listReviewLogs(active).catch(() => []),
        getActiveVocabularyReviewSession().catch(() => ({ active: false })),
      ]);
    setProvider(active);
    setProviderSetting(
      active === "local" && selected !== setting.local_default_wordbook_id
        ? { ...setting, local_default_wordbook_id: selected }
        : setting,
    );
    if (active === "local" && selected) setWordbookId(selected);
    setItems(words);
    setAnki(status);
    setBooks(nextBooks);
    setStats(nextStats);
    setAnkiDecks(nextDecks);
    setLogs(nextLogs);
    setHasActiveReview(activeReview.active);
  }, [desktop, search, state, wordbookId]);
  useEffect(() => {
    void load();
  }, [load]);
  const start = async () => {
    try {
      setReviewReport(null);
      const batch = await startVocabularyReviewSession(5);
      setReviewSessionId(batch.session.id);
      setReviewQueue(batch.questions || []);
      setQuestion(batch.questions?.[0] || null);
      setRevealed(false);
      if (!batch.questions?.length) message.info("当前词本没有到期单词");
    } catch (error) {
      message.error(error instanceof Error ? error.message : "无法开始复习");
    }
  };
  const rate = async (rating?: string, response?: string) => {
    if (!question || !reviewSessionId) return;
    try {
      await submitVocabularySessionReview(reviewSessionId, {
        word_id: question.word?.id,
        term: question.word?.term || question.prompt,
        card_id: question.card_id,
        rating,
        response,
        row_version: question.row_version,
        idempotency_key: crypto.randomUUID(),
        previewed_at: question.previewed_at,
      });
      const remaining = reviewQueue.slice(1);
      setRevealed(false);
      setObjectiveAnswer("");
      if (remaining.length) {
        setReviewQueue(remaining);
        setQuestion(remaining[0]);
      } else {
        const next = await startVocabularyReviewSession(5);
        if (next.questions?.length) {
          setReviewQueue(next.questions);
          setQuestion(next.questions[0]);
          setReviewSessionId(next.session.id);
        } else {
          const report = await completeVocabularyReviewSession(reviewSessionId);
          setQuestion(null);
          setReviewQueue([]);
          setReviewReport(report);
          setReviewSessionId("");
          setHasActiveReview(false);
        }
      }
      void load();
    } catch (error) {
      message.error(
        error instanceof Error ? error.message : "提交复习结果失败",
      );
    }
  };
  const cardTypeLabel = (type?: string) =>
    ({
      word_to_meaning: t("vocabulary.cardTypes.wordToMeaning"),
      meaning_to_word: t("vocabulary.cardTypes.meaningToWord"),
      sentence_cloze: t("vocabulary.cardTypes.sentenceCloze"),
    })[type || ""] || t("vocabulary.cardTypes.anki");
  const batchReset = (scope: "today" | "wordbook") =>
    Modal.confirm({
      title: scope === "today" ? "重置今日学习？" : "重置当前单词本？",
      content:
        scope === "today"
          ? "今天复习过的单词将回到初始学习状态。"
          : "当前单词本内的所有单词将回到初始学习状态。",
      okText: "确认重置",
      onOk: async () => {
        const result = await resetVocabularyBatch(
          scope,
          scope === "wordbook" ? wordbookId : undefined,
        );
        message.success(`已重置 ${result.reset} 个单词`);
        await load();
      },
    });
  const edit = () => {
    if (!detail) return;
    form.setFieldsValue({
      ...detail.word,
      tags: detail.tags,
      wordbook_ids: detail.wordbooks.map((x) => x.id),
    });
    setEditOpen(true);
  };
  const saveEdit = async () => {
    if (!detail) return;
    await updateVocabularyWord(detail.word.id, await form.validateFields());
    setEditOpen(false);
    setDetail(null);
    void load();
  };
  const createBook = () => {
    let name = "";
    Modal.confirm({
      title: "新建单词本",
      content: (
        <Input
          autoFocus
          placeholder="输入单词本名称"
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            name = event.target.value;
          }}
        />
      ),
      onOk: async () => {
        if (!name.trim()) throw new Error("请输入名称");
        if (provider === "anki") {
          await createAnkiDeck(name);
          setProviderSetting(
            await saveVocabularyProvider({
              ...providerSetting,
              anki_deck_name: name,
            }),
          );
        } else {
          const book = await createWordbook({ name });
          setProviderSetting(
            await saveVocabularyProvider({
              ...providerSetting,
              local_default_wordbook_id: book.id,
            }),
          );
          setWordbookId(book.id);
        }
        await load();
      },
    });
  };
  const removeBook = (id: string, name: string) => {
    let mode: "move" | "delete_words" = "move";
    const localTargets = books.filter((book) => book.id !== id);
    const ankiTargets = ankiDecks.filter((deck) => String(deck.id) !== id);
    let target =
      provider === "anki"
        ? ankiTargets.find((deck) => deck.is_default)?.name ||
          ankiTargets[0]?.name
        : localTargets.find((book) => book.name === "默认生词本")?.id ||
          localTargets[0]?.id;
    Modal.confirm({
      title: `删除单词本「${name}」？`,
      width: 520,
      content: (
        <Space direction="vertical" style={{ width: "100%" }}>
          <Radio.Group
            defaultValue="move"
            onChange={(event: {
              target: { value: "move" | "delete_words" };
            }) => {
              mode = event.target.value;
            }}
          >
            <Space direction="vertical">
              <Radio value="move">把其中的单词移动到</Radio>
              <Radio value="delete_words">同时删除其中的所有单词</Radio>
            </Space>
          </Radio.Group>
          <Select
            defaultValue={target}
            style={{ width: "100%" }}
            options={
              provider === "anki"
                ? ankiTargets.map((deck) => ({
                    value: deck.name,
                    label: deck.name,
                  }))
                : localTargets.map((book) => ({
                    value: book.id,
                    label: book.name,
                  }))
            }
            onChange={(value: string) => {
              target = value;
            }}
          />
        </Space>
      ),
      okText: "确认删除",
      okButtonProps: { danger: true },
      onOk: async () => {
        if (mode === "move" && !target) throw new Error("请选择目标单词本");
        if (provider === "anki") await deleteAnkiDeck(name, mode, target);
        else await deleteWordbook(id, mode, target);
        if (provider === "anki" && providerSetting.anki_deck_name === name)
          setProviderSetting(
            await saveVocabularyProvider({
              ...providerSetting,
              anki_deck_name: target || ankiTargets[0]?.name || "",
            }),
          );
        if (
          provider === "local" &&
          (wordbookId || providerSetting.local_default_wordbook_id) === id
        ) {
          setWordbookId(target);
          setProviderSetting(
            await saveVocabularyProvider({
              ...providerSetting,
              local_default_wordbook_id: target,
            }),
          );
        }
        await load();
        message.success("单词本已删除");
      },
    });
  };
  const selectBook = async (value: string) => {
    if (provider === "anki")
      setProviderSetting(
        await saveVocabularyProvider({
          ...providerSetting,
          anki_deck_name: value,
        }),
      );
    else {
      setWordbookId(value);
      setProviderSetting(
        await saveVocabularyProvider({
          ...providerSetting,
          local_default_wordbook_id: value,
        }),
      );
    }
  };
  const selectBackend = async (next: "anki" | "local") => {
    if (next === "anki" && !anki?.connected) return;
    const saved = await saveVocabularyProvider({
      ...providerSetting,
      selected_provider: next,
    });
    setProviderSetting(saved);
    setProvider(next);
    setWordbookId(undefined);
    void load();
  };
  const createWord = async () => {
    const values = await newWordForm.validateFields();
    await addVocabularyWord({
      ...values,
      provider,
      wordbook_ids:
        provider === "local"
          ? [
              wordbookId || providerSetting.local_default_wordbook_id || "",
            ].filter(Boolean)
          : undefined,
      origin_type: "user",
    });
    setNewWordOpen(false);
    newWordForm.resetFields();
    message.success("已加入生词表");
    void load();
  };
  const columns = useMemo(
    () => [
      {
        title: "单词",
        render: (_: unknown, x: VocabularyListItem) => (
          <Button type="link" onClick={() => setDetail(x)}>
            {x.word.term}
          </Button>
        ),
      },
      {
        title: "释义 / 词性",
        render: (_: unknown, x: VocabularyListItem) => (
          <div>
            {x.word.meaning || x.example?.translation || "—"}
            <Typography.Text type="secondary">
              　{x.word.part_of_speech}
            </Typography.Text>
            {x.example?.sentence ? (
              <div>
                <Typography.Text type="secondary">
                  {x.example.sentence}
                </Typography.Text>
              </div>
            ) : null}
          </div>
        ),
      },
      {
        title: "来源 / 标签",
        render: (_: unknown, x: VocabularyListItem) => (
          <Space wrap>
            {x.word.source_name || x.word.origin_type}
            {x.tags?.map((t) => (
              <Tag key={t}>{t}</Tag>
            ))}
          </Space>
        ),
      },
      {
        title: "状态",
        render: (_: unknown, x: VocabularyListItem) => (
          <Tag
            color={
              x.state === "mastered" ? "green" : x.lapses > 2 ? "red" : "blue"
            }
          >
            {labels[x.state] || x.state}
          </Tag>
        ),
      },
      {
        title: "复习",
        render: (_: unknown, x: VocabularyListItem) => (
          <span>
            {x.reps} 次 · 错 {x.lapses}
          </span>
        ),
      },
      {
        title: "下次复习",
        render: (_: unknown, x: VocabularyListItem) =>
          x.due_at ? new Date(x.due_at).toLocaleString() : "—",
      },
      {
        title: "操作",
        render: (_: unknown, x: VocabularyListItem) => (
          <Space>
            {x.state === "mastered" ? (
              <Button
                size="small"
                onClick={() => void resumeVocabulary(x.word.id).then(load)}
              >
                恢复
              </Button>
            ) : (
              <Button
                size="small"
                onClick={() => void masterVocabulary(x.word.id).then(load)}
              >
                标记学会
              </Button>
            )}
            <Dropdown
              trigger={["click"]}
              menu={{
                items: [
                  ...(provider === "local"
                    ? [
                        {
                          key: "reset",
                          label: "重置学习状态",
                          onClick: () =>
                            void resetVocabulary(x.word.id).then(load),
                        },
                      ]
                    : []),
                  {
                    key: "delete",
                    danger: true,
                    label: "删除单词",
                    onClick: () =>
                      Modal.confirm({
                        title: `删除 ${x.word.term}？`,
                        content: "相关例句、来源和复习历史也会删除。",
                        onOk: () => deleteVocabulary(x.word.id).then(load),
                      }),
                  },
                ],
              }}
            >
              <Button
                size="small"
                icon={<MoreOutlined />}
                aria-label="更多操作"
              />
            </Dropdown>
          </Space>
        ),
      },
    ],
    [load, provider],
  );
  const visibleLogs = useMemo(() => {
    const now = Date.now(),
      days = logRange === "today" ? 1 : logRange === "week" ? 7 : 30;
    return logs.filter(
      (x) => now - new Date(x.reviewed_at).getTime() < days * 86400000,
    );
  }, [logs, logRange]);
  const correct = visibleLogs.filter((x) => x.rating > 1).length;
  const bookOptions = (
    provider === "anki"
      ? ankiDecks.map((deck) => ({
          value: deck.name,
          id: String(deck.id),
          name: deck.name,
          locked: deck.is_default,
        }))
      : books.map((book) => ({
          value: book.id,
          id: book.id,
          name: book.name,
          locked: book.name === "默认生词本",
        }))
  ).map((option) => ({
    value: option.value,
    label: (
      <span
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <span>{option.name}</span>
        {option.locked ? (
          <Typography.Text type="secondary">系统默认</Typography.Text>
        ) : (
          <CloseOutlined
            aria-label={`删除 ${option.name}`}
            onMouseDown={(event: MouseEvent<HTMLElement>) => {
              event.preventDefault();
              event.stopPropagation();
              removeBook(option.id, option.name);
            }}
          />
        )}
      </span>
    ),
  }));
  const selectedBook =
    provider === "anki"
      ? providerSetting.anki_deck_name
      : wordbookId || providerSetting.local_default_wordbook_id;
  const wordsView = (
    <Space direction="vertical" size={18} style={{ width: "100%" }}>
      <Panel>
        <Space
          wrap
          style={{ display: "flex", justifyContent: "space-between" }}
        >
          <Space>
            <Typography.Text strong>我的词本</Typography.Text>
            <Select
              value={selectedBook}
              placeholder="选择单词本"
              style={{ width: 260 }}
              onChange={selectBook}
              options={bookOptions}
            />
          </Space>
          <Space>
            <Button
              icon={<PlusOutlined />}
              onClick={() => setNewWordOpen(true)}
            >
              新建单词
            </Button>
            <Button
              type="primary"
              onClick={() => void start()}
              disabled={
                provider === "anki" && anki?.review_capability !== "full"
              }
            >
              {hasActiveReview ? "继续复习" : "开始复习"}
            </Button>
            <Button onClick={createBook}>新建单词本</Button>
            {provider === "local" ? (
              <Dropdown
                menu={{
                  items: [
                    {
                      key: "today",
                      label: "重置今日学习",
                      onClick: () => batchReset("today"),
                    },
                    {
                      key: "book",
                      label: "重置当前单词本",
                      danger: true,
                      onClick: () => batchReset("wordbook"),
                    },
                  ],
                }}
              >
                <Button icon={<MoreOutlined />}>批量操作</Button>
              </Dropdown>
            ) : null}
          </Space>
        </Space>
        {stats ? (
          <div
            style={{
              display: "flex",
              justifyContent: "space-around",
              marginTop: 20,
              padding: 16,
              background: "#f7f9fc",
              borderRadius: 8,
            }}
          >
            {[
              ["全部", stats.total],
              ["今日到期", stats.due],
              ["新词", stats.new],
              ["今日完成", stats.reviewed_today],
              ["易错", stats.weak],
            ].map(([title, value]) => (
              <Statistic key={String(title)} title={title} value={value} />
            ))}
          </div>
        ) : null}
        <Space wrap style={{ marginTop: 20 }}>
          <Input.Search
            allowClear
            placeholder="搜索单词或释义"
            onSearch={setSearch}
            style={{ width: 320 }}
          />
          <Select
            allowClear
            placeholder="记忆状态"
            value={state}
            onChange={setState}
            style={{ width: 150 }}
            options={Object.entries(labels).map(([value, label]) => ({
              value,
              label,
            }))}
          />
        </Space>
        {provider === "anki" && anki?.review_capability !== "full" ? (
          <Alert
            style={{ marginTop: 16 }}
            type="info"
            showIcon
            message="当前 AnkiConnect 不支持可靠评分，请在 Anki 中复习；生词管理仍可使用。"
          />
        ) : null}
        <Table<VocabularyListItem>
          style={{ marginTop: 16 }}
          rowKey={(x: VocabularyListItem) => x.word.id}
          dataSource={items}
          locale={{ emptyText: <Empty description="暂无生词" /> }}
          columns={columns}
        />
      </Panel>
    </Space>
  );
  const historyColumns = [
    {
      title: "时间",
      dataIndex: "reviewed_at",
      render: (x: string) => new Date(x).toLocaleString(),
    },
    { title: "单词", dataIndex: "term" },
    {
      title: "题型",
      dataIndex: "card_type",
      render: (x: string) => cardTypeLabel(x),
    },
    {
      title: "结果",
      dataIndex: "rating",
      render: (x: number) => (
        <Tag color={x === 1 ? "red" : "green"}>{ratings[x] || x}</Tag>
      ),
    },
    {
      title: "间隔变化",
      render: (_: unknown, x: ReviewLog) => {
        const [a, b] = logIntervals(x);
        return b === undefined ? `${a} 天 → —` : `${a} 天 → ${b} 天`;
      },
    },
    {
      title: "下次复习",
      render: (_: unknown, x: ReviewLog) =>
        "due_at" in (x.fsrs_log || {}) && x.fsrs_log.due_at
          ? new Date(x.fsrs_log.due_at).toLocaleDateString()
          : "—",
    },
  ];
  const historyView = (
    <Panel>
      <Space direction="vertical" size={18} style={{ width: "100%" }}>
        <Space style={{ display: "flex", justifyContent: "space-between" }}>
          <Radio.Group
            value={logRange}
            onChange={(event: { target: { value: string } }) =>
              setLogRange(event.target.value)
            }
            optionType="button"
            buttonStyle="solid"
            options={[
              { label: "今天", value: "today" },
              { label: "本周", value: "week" },
              { label: "本月", value: "month" },
            ]}
          />
          <Button href={reviewLogsExportUrl(provider)}>导出记录</Button>
        </Space>
        <div
          style={{
            display: "flex",
            justifyContent: "space-around",
            padding: 16,
            background: "#f7f9fc",
            borderRadius: 8,
          }}
        >
          <Statistic title="作答次数" value={visibleLogs.length} />
          <Statistic title="答对" value={correct} />
          <Statistic title="答错" value={visibleLogs.length - correct} />
          <Statistic
            title="正确率"
            value={
              visibleLogs.length
                ? Math.round((correct / visibleLogs.length) * 100)
                : 0
            }
            suffix="%"
          />
        </div>
        <Table<ReviewLog>
          rowKey={(x: ReviewLog) => `${x.card_id}-${x.reviewed_at}`}
          dataSource={visibleLogs}
          columns={historyColumns}
          locale={{ emptyText: <Empty description="暂无学习记录" /> }}
        />
      </Space>
    </Panel>
  );
  return (
    <div style={{ padding: 24, maxWidth: 1500, margin: "0 auto" }}>
      <Space direction="vertical" size={18} style={{ width: "100%" }}>
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <div>
            <Typography.Title level={2} style={{ marginBottom: 4 }}>
              {tab === "words" ? "生词表" : "学习记录"}
            </Typography.Title>
            <Typography.Text type="secondary">
              {tab === "words"
                ? "管理生词并开始复习。"
                : "查看作答结果与记忆间隔变化。"}
            </Typography.Text>
          </div>
          <Button
            icon={<SettingOutlined />}
            onClick={() => setSettingsOpen(true)}
          >
            设置
          </Button>
        </div>
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={[
            { key: "words", label: "生词表", children: wordsView },
            { key: "history", label: "学习记录", children: historyView },
          ]}
        />
      </Space>
      <Modal
        open={settingsOpen}
        title="生词表设置"
        footer={null}
        width={760}
        onCancel={() => setSettingsOpen(false)}
      >
        <Typography.Title level={5}>存储与学习方式</Typography.Title>
        <div
          style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}
        >
          <Panel
            onClick={() => void selectBackend("local")}
            style={{
              borderColor: provider === "local" ? "#1677ff" : undefined,
              cursor: "pointer",
            }}
          >
            <Space>
              <Radio checked={provider === "local"} />
              <BookOutlined style={{ fontSize: 24 }} />
              <div>
                <Typography.Text strong>LazyMind 本地</Typography.Text>
                <div>
                  <Typography.Text type="secondary">
                    使用内置 FSRS 安排复习
                  </Typography.Text>
                </div>
              </div>
            </Space>
          </Panel>
          <Panel
            onClick={() => void selectBackend("anki")}
            style={{
              borderColor: provider === "anki" ? "#1677ff" : undefined,
              opacity: anki?.connected ? 1 : 0.75,
              cursor: anki?.connected ? "pointer" : "default",
            }}
          >
            <Space>
              <Radio
                checked={provider === "anki"}
                disabled={!anki?.connected}
              />
              <CloudSyncOutlined style={{ fontSize: 24 }} />
              <div>
                <Typography.Text strong>Anki</Typography.Text>
                <div>
                  <Typography.Text
                    type={anki?.connected ? "success" : "secondary"}
                  >
                    {anki?.connected ? "已连接" : "尚未配置"}
                  </Typography.Text>
                </div>
              </div>
            </Space>
          </Panel>
        </div>
        {!anki?.connected ? (
          <Alert
            style={{ marginTop: 16 }}
            type="warning"
            showIcon
            message="使用 Anki 前需要完成连接"
            action={
              <Button href="/settings?section=external_apps">
                前往外部应用
              </Button>
            }
          />
        ) : null}
        {provider === "anki" ? (
          <Alert
            style={{ marginTop: 16 }}
            type="info"
            showIcon
            message="数据会直接写入 Anki"
            description="新增单词、修改词本和复习评分都会通过 AnkiConnect 立即生效，无需手动同步。"
          />
        ) : null}
      </Modal>
      <Modal
        open={newWordOpen}
        title="新建单词"
        onCancel={() => setNewWordOpen(false)}
        onOk={() => void createWord()}
      >
        <Form form={newWordForm} layout="vertical">
          <Form.Item name="term" label="单词" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="meaning" label="释义" rules={[{ required: true }]}>
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="part_of_speech" label="词性">
            <Input />
          </Form.Item>
          <Form.Item name="sentence" label="例句">
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="translation" label="例句翻译">
            <Input.TextArea />
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        className="vocabulary-review-modal"
        open={!!question}
        title={
          <span>
            复习{" "}
            <Typography.Text type="secondary">
              {reviewQueue.length ? `${reviewQueue.length} 题待完成` : ""}
            </Typography.Text>
          </span>
        }
        footer={null}
        onCancel={() => {
          setQuestion(null);
          setReviewSessionId("");
          setReviewQueue([]);
        }}
      >
        {question ? (
          <div className="review-card">
            <Tag color="blue">{cardTypeLabel(question.card_type)}</Tag>
            <Progress
              percent={Math.round(((5 - reviewQueue.length) / 5) * 100)}
              showInfo={false}
            />
            <Typography.Title level={2}>{question.prompt}</Typography.Title>
            {question.interaction === "multiple_choice" ? (
              <div className="review-choices">
                {question.choices?.map((choice) => (
                  <Button
                    key={`${choice.value}-${choice.part_of_speech}`}
                    onClick={() => void rate(undefined, choice.value)}
                  >
                    <span>{choice.label}</span>
                    {choice.part_of_speech ? (
                      <Tag>{choice.part_of_speech}</Tag>
                    ) : null}
                  </Button>
                ))}
              </div>
            ) : question.interaction === "text_input" ? (
              <div className="review-input">
                <Input
                  size="large"
                  value={objectiveAnswer}
                  placeholder="输入完整的英语单词"
                  onChange={(event) => setObjectiveAnswer(event.target.value)}
                  onPressEnter={() =>
                    objectiveAnswer.trim() &&
                    void rate(undefined, objectiveAnswer)
                  }
                />
                <Button
                  size="large"
                  type="primary"
                  disabled={!objectiveAnswer.trim()}
                  onClick={() => void rate(undefined, objectiveAnswer)}
                >
                  提交答案
                </Button>
                <Typography.Text type="secondary">
                  忽略大小写，拼写必须完全正确
                </Typography.Text>
              </div>
            ) : revealed ? (
              <>
                <div className="review-answer">{question.answer}</div>
                <div className="review-ratings">
                  {(["again", "hard", "good", "easy"] as const).map((r) => (
                    <Button
                      key={r}
                      danger={r === "again"}
                      type={r === "good" ? "primary" : "default"}
                      onClick={() => void rate(r)}
                    >
                      <strong>
                        {
                          {
                            again: "忘记",
                            hard: "困难",
                            good: "记得",
                            easy: "简单",
                          }[r]
                        }
                      </strong>
                      <small>{interval(question.options?.[r])}</small>
                    </Button>
                  ))}
                </div>
              </>
            ) : (
              <Button
                size="large"
                type="primary"
                onClick={() => setRevealed(true)}
              >
                显示答案
              </Button>
            )}
          </div>
        ) : null}
      </Modal>
      <Modal
        className="vocabulary-report-modal"
        open={!!reviewReport}
        title={null}
        onCancel={() => setReviewReport(null)}
        footer={null}
      >
        {reviewReport ? (
          <div className="review-report">
            <div className="report-icon">
              <TrophyOutlined />
            </div>
            <Typography.Title level={2}>本轮复习完成</Typography.Title>
            <Typography.Text type="secondary">
              坚持得很好，复习结果已保存
            </Typography.Text>
            <div className="report-score">
              <Progress
                type="circle"
                percent={Math.round((reviewReport.accuracy || 0) * 100)}
                size={112}
              />
              <div>
                <Statistic
                  title="完成题目"
                  value={reviewReport.total || 0}
                  suffix="题"
                />
                <Typography.Text type="secondary">
                  平均间隔{" "}
                  {(reviewReport.average_interval_before_days || 0).toFixed(1)}{" "}
                  天 →{" "}
                  {(reviewReport.average_interval_after_days || 0).toFixed(1)}{" "}
                  天
                </Typography.Text>
              </div>
            </div>
            <div className="report-breakdown">
              <span>
                <CheckCircleFilled /> 记得 {reviewReport.correct || 0}
              </span>
              <span>需巩固 {reviewReport.incorrect || 0}</span>
            </div>
            {reviewReport.difficult_words?.length ? (
              <Alert
                type="warning"
                showIcon
                message={`建议继续巩固：${reviewReport.difficult_words.join("、")}`}
              />
            ) : null}
            <Button
              block
              size="large"
              type="primary"
              onClick={() => setReviewReport(null)}
            >
              完成
            </Button>
          </div>
        ) : null}
      </Modal>
      <Drawer
        open={!!detail}
        title={detail?.word.term}
        onClose={() => setDetail(null)}
        extra={<Button onClick={edit}>编辑</Button>}
      >
        <Descriptions
          column={1}
          items={[
            {
              key: "meaning",
              label: "释义",
              children: detail?.word.meaning || "—",
            },
            {
              key: "definition",
              label: "补充释义",
              children: detail?.word.definition || "—",
            },
            {
              key: "example",
              label: "例句",
              children: detail?.example?.sentence || "—",
            },
            {
              key: "translation",
              label: "例句译文",
              children: detail?.example?.translation || "—",
            },
            {
              key: "source",
              label: "内容来源",
              children: detail?.word.source_name || detail?.word.origin_type,
            },
            {
              key: "state",
              label: "复习状态",
              children: detail ? labels[detail.state] || detail.state : "",
            },
          ]}
        />
      </Drawer>
      <Modal
        open={editOpen}
        title="编辑单词"
        onCancel={() => setEditOpen(false)}
        onOk={() => void saveEdit()}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="term" label="单词" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="phonetic" label="音标">
            <Input />
          </Form.Item>
          <Form.Item name="part_of_speech" label="词性">
            <Input />
          </Form.Item>
          <Form.Item name="meaning" label="基础释义">
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="definition" label="补充释义">
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="user_note" label="备注">
            <Input.TextArea />
          </Form.Item>
          {provider === "local" ? (
            <Form.Item name="wordbook_ids" label="单词本">
              <Select
                mode="multiple"
                options={books.map((b) => ({ value: b.id, label: b.name }))}
              />
            </Form.Item>
          ) : null}
          <Form.Item name="tags" label="标签">
            <Select mode="tags" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
