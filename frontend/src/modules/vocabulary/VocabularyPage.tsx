import {
  CheckCircleFilled,
  CloseOutlined,
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
import {
  addVocabularyWord,
  completeVocabularyReviewSession,
  createWordbook,
  deleteVocabulary,
  deleteWordbook,
  getActiveVocabularyReviewSession,
  getVocabularyProvider,
  getVocabularyStats,
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
  type ReviewLog,
  type ReviewQuestion,
  type ReviewSessionReport,
  type ReviewStats,
  type VocabularyListItem,
  type Wordbook,
} from "./api";
import "./VocabularyPage.scss";

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
  const labels = useMemo<Record<string, string>>(() => ({
    new: t("vocabularyPage.new"),
    learning: t("vocabularyPage.learning"),
    review: t("vocabularyPage.reviewing"),
    relearning: t("vocabularyPage.relearning"),
    mastered: t("vocabularyPage.mastered"),
    suspended: t("vocabularyPage.suspended"),
  }), [t]);
  const ratings: Record<number, string> = {
    1: t("vocabularyPage.incorrectRating"),
    2: t("vocabularyPage.hard"),
    3: t("vocabularyPage.goodRating"),
    4: t("vocabularyPage.easy"),
  };
  const interval = (value?: string) => {
    if (!value) return "";
    const ms = new Date(value).getTime() - Date.now();
    if (ms < 3600000) return t("vocabularyPage.minutes", { count: Math.max(1, Math.round(ms / 60000)) });
    if (ms < 86400000) return t("vocabularyPage.hours", { count: Math.round(ms / 3600000) });
    return t("vocabularyPage.days", { count: Math.round(ms / 86400000) });
  };
  const [tab, setTab] = useState("words");
  const [newWordOpen, setNewWordOpen] = useState(false);
  const [items, setItems] = useState<VocabularyListItem[]>([]);
  const [stats, setStats] = useState<ReviewStats | null>(null);
  const [books, setBooks] = useState<Wordbook[]>([]);
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
  const load = useCallback(async (preferredWordbookId?: string) => {
    const [setting, allBooks] = await Promise.all([
      getVocabularyProvider(),
      listWordbooks(),
    ]);
    const nextBooks = allBooks.filter((book) => !book.capability_key || book.capability_key === "english_definition");
    let selected = preferredWordbookId || wordbookId || setting.local_default_wordbook_id || "";
    if (!nextBooks.some((book) => book.id === selected)) {
      selected = nextBooks.find((book) => book.name === "默认生词本")?.id || nextBooks[0]?.id || "";
    }
    const [words, nextStats, nextLogs, activeReview] = await Promise.all([
      listVocabulary("local", { search, state: state || "", wordbook_id: selected }),
      getVocabularyStats().catch(() => null),
      listReviewLogs().catch(() => []),
      getActiveVocabularyReviewSession().catch(() => ({ active: false })),
    ]);
    setWordbookId(selected || undefined);
    setItems(words);
    setBooks(nextBooks);
    setStats(nextStats);
    setLogs(nextLogs);
    setHasActiveReview(activeReview.active);
  }, [search, state, wordbookId]);
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
      if (!batch.questions?.length) message.info(t("vocabularyPage.noDueWords"));
    } catch (error) {
      message.error(error instanceof Error ? error.message : t("vocabularyPage.reviewStartFailed"));
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
        error instanceof Error ? error.message : t("vocabularyPage.reviewSubmitFailed"),
      );
    }
  };
  const cardTypeLabel = (type?: string) =>
    ({
      word_to_meaning: t("vocabulary.cardTypes.wordToMeaning"),
      meaning_to_word: t("vocabulary.cardTypes.meaningToWord"),
      sentence_cloze: t("vocabulary.cardTypes.sentenceCloze"),
    })[type || ""] || t("vocabularyPage.review");
  const batchReset = (scope: "today" | "wordbook") =>
    Modal.confirm({
      title: scope === "today" ? t("vocabularyPage.resetTodayTitle") : t("vocabularyPage.resetBookTitle"),
      content:
        scope === "today"
          ? t("vocabularyPage.resetTodayDescription")
          : t("vocabularyPage.resetBookDescription"),
      okText: t("vocabularyPage.confirmReset"),
      onOk: async () => {
        const result = await resetVocabularyBatch(
          scope,
          scope === "wordbook" ? wordbookId : undefined,
        );
        message.success(t("vocabularyPage.wordsReset", { count: result.reset }));
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
    const capabilityKey = "english_definition";
    const questionTypes = ["single_choice", "text_input", "cloze"];
    Modal.confirm({
      title: t("vocabularyPage.newBook"),
      content: (
        <Input autoFocus placeholder={t("vocabularyPage.inputName")} onChange={(event: ChangeEvent<HTMLInputElement>) => { name = event.target.value; }} />
      ),
      onOk: async () => {
        if (!name.trim()) throw new Error(t("vocabularyPage.nameRequired"));
        const book = await createWordbook({ name, capability_key: capabilityKey, question_types: questionTypes });
        await saveVocabularyProvider({ local_default_wordbook_id: book.id });
        setWordbookId(book.id);
        await load(book.id);
      },
    });
  };
  const removeBook = (id: string, name: string) => {
    let mode: "move" | "delete_words" = "move";
    const localTargets = books.filter((book) => book.id !== id);
    let target = localTargets.find((book) => book.name === "默认生词本")?.id || localTargets[0]?.id;
    Modal.confirm({
      title: t("vocabularyPage.deleteBookTitle", { name }),
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
              <Radio value="move">{t("vocabularyPage.moveWords")}</Radio>
              <Radio value="delete_words">{t("vocabularyPage.deleteWords")}</Radio>
            </Space>
          </Radio.Group>
          <Select
            defaultValue={target}
            style={{ width: "100%" }}
            options={localTargets.map((book) => ({ value: book.id, label: book.name }))}
            onChange={(value: string) => {
              target = value;
            }}
          />
        </Space>
      ),
      okText: t("vocabularyPage.confirmDelete"),
      okButtonProps: { danger: true },
      onOk: async () => {
        if (mode === "move" && !target) throw new Error(t("vocabularyPage.selectTarget"));
        await deleteWordbook(id, mode, target);
        if (wordbookId === id) {
          await saveVocabularyProvider({ local_default_wordbook_id: target || "" });
          setWordbookId(target);
          await load(target);
        } else {
          await load();
        }
        message.success(t("vocabularyPage.bookDeleted"));
      },
    });
  };
  const selectBook = async (value: string) => {
    setWordbookId(value);
    await saveVocabularyProvider({ local_default_wordbook_id: value });
  };
  const createWord = async () => {
    const values = await newWordForm.validateFields();
    await addVocabularyWord({
      ...values,
      provider: "local",
      wordbook_ids: wordbookId ? [wordbookId] : undefined,
      origin_type: "user",
    });
    setNewWordOpen(false);
    newWordForm.resetFields();
    message.success(t("vocabularyPage.wordAdded"));
    void load();
  };
  const columns = useMemo(
    () => [
      {
        title: t("vocabularyPage.word"),
        render: (_: unknown, x: VocabularyListItem) => (
          <Button type="link" onClick={() => setDetail(x)}>
            {x.word.term}
          </Button>
        ),
      },
      {
        title: t("vocabularyPage.meaningAndPartOfSpeech"),
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
        title: t("vocabularyPage.sourceAndTags"),
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
        title: t("vocabularyPage.status"),
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
        title: t("vocabularyPage.review"),
        render: (_: unknown, x: VocabularyListItem) => (
          <span>
            {t("vocabularyPage.reviewCounts", { reps: x.reps, lapses: x.lapses })}
          </span>
        ),
      },
      {
        title: t("vocabularyPage.nextReview"),
        render: (_: unknown, x: VocabularyListItem) =>
          x.due_at ? new Date(x.due_at).toLocaleString() : "—",
      },
      {
        title: t("vocabularyPage.actions"),
        render: (_: unknown, x: VocabularyListItem) => (
          <Space>
            {x.state === "mastered" ? (
              <Button
                size="small"
                onClick={() => void resumeVocabulary(x.word.id).then(() => load())}
              >
                {t("vocabularyPage.resume")}
              </Button>
            ) : (
              <Button
                size="small"
                onClick={() => void masterVocabulary(x.word.id).then(() => load())}
              >
                {t("vocabularyPage.markMastered")}
              </Button>
            )}
            <Dropdown
              trigger={["click"]}
              menu={{
                items: [
                  {
                    key: "reset",
                    label: t("vocabularyPage.resetState"),
                    onClick: () => void resetVocabulary(x.word.id).then(() => load()),
                  },
                  {
                    key: "delete",
                    danger: true,
                    label: t("vocabularyPage.deleteWord"),
                    onClick: () =>
                      Modal.confirm({
                        title: t("vocabularyPage.deleteWordTitle", { term: x.word.term }),
                        content: t("vocabularyPage.deleteWordDescription"),
                        onOk: () => deleteVocabulary(x.word.id).then(() => load()),
                      }),
                  },
                ],
              }}
            >
              <Button
                size="small"
                icon={<MoreOutlined />}
                aria-label={t("vocabularyPage.moreActions")}
              />
            </Dropdown>
          </Space>
        ),
      },
    ],
    [labels, load, t],
  );
  const visibleLogs = useMemo(() => {
    const now = Date.now(),
      days = logRange === "today" ? 1 : logRange === "week" ? 7 : 30;
    return logs.filter(
      (x) => now - new Date(x.reviewed_at).getTime() < days * 86400000,
    );
  }, [logs, logRange]);
  const correct = visibleLogs.filter((x) => x.rating > 1).length;
  const bookOptions = books.map((book) => ({
    id: book.id,
    value: book.id,
    name: book.name,
    locked: book.name === "默认生词本",
  })).map((option) => ({
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
          <Typography.Text type="secondary">{t("vocabularyPage.systemDefault")}</Typography.Text>
        ) : (
          <CloseOutlined
            aria-label={t("vocabularyPage.deleteBookLabel", { name: option.name })}
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
  const wordsView = (
    <Space direction="vertical" size={18} style={{ width: "100%" }}>
      <Panel>
        <Space
          wrap
          style={{ display: "flex", justifyContent: "space-between" }}
        >
          <Space>
            <Typography.Text strong>{t("vocabularyPage.myBooks")}</Typography.Text>
            <Select
              value={wordbookId}
              placeholder={t("vocabularyPage.selectBook")}
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
              {t("vocabularyPage.newWord")}
            </Button>
            <Button
              type="primary"
              onClick={() => void start()}
            >
              {hasActiveReview ? t("vocabularyPage.resumeReview") : t("vocabularyPage.startReview")}
            </Button>
            <Button onClick={createBook}>{t("vocabularyPage.newBook")}</Button>
            <Dropdown
              menu={{
                items: [
                  {
                    key: "today",
                    label: t("vocabularyPage.resetToday"),
                    onClick: () => batchReset("today"),
                  },
                  {
                    key: "book",
                    label: t("vocabularyPage.resetBook"),
                    danger: true,
                    onClick: () => batchReset("wordbook"),
                  },
                ],
              }}
            >
              <Button icon={<MoreOutlined />}>{t("vocabularyPage.batchActions")}</Button>
            </Dropdown>
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
              [t("vocabularyPage.all"), stats.total],
              [t("vocabularyPage.dueToday"), stats.due],
              [t("vocabularyPage.new"), stats.new],
              [t("vocabularyPage.reviewedToday"), stats.reviewed_today],
              [t("vocabularyPage.weak"), stats.weak],
            ].map(([title, value]) => (
              <Statistic key={String(title)} title={title} value={value} />
            ))}
          </div>
        ) : null}
        <Space wrap style={{ marginTop: 20 }}>
          <Input.Search
            allowClear
            placeholder={t("vocabularyPage.search")}
            onSearch={setSearch}
            style={{ width: 320 }}
          />
          <Select
            allowClear
            placeholder={t("vocabularyPage.memoryState")}
            value={state}
            onChange={setState}
            style={{ width: 150 }}
            options={Object.entries(labels).map(([value, label]) => ({
              value,
              label,
            }))}
          />
        </Space>
        <Table<VocabularyListItem>
          style={{ marginTop: 16 }}
          rowKey={(x: VocabularyListItem) => x.word.id}
          dataSource={items}
          locale={{ emptyText: <Empty description={t("vocabularyPage.empty")} /> }}
          columns={columns}
        />
      </Panel>
    </Space>
  );
  const historyColumns = [
    {
      title: t("vocabularyPage.time"),
      dataIndex: "reviewed_at",
      render: (x: string) => new Date(x).toLocaleString(),
    },
    { title: t("vocabularyPage.word"), dataIndex: "term" },
    {
      title: t("vocabularyPage.questionType"),
      dataIndex: "card_type",
      render: (x: string) => cardTypeLabel(x),
    },
    {
      title: t("vocabularyPage.result"),
      dataIndex: "rating",
      render: (x: number) => (
        <Tag color={x === 1 ? "red" : "green"}>{ratings[x] || x}</Tag>
      ),
    },
    {
      title: t("vocabularyPage.intervalChange"),
      render: (_: unknown, x: ReviewLog) => {
        const [a, b] = logIntervals(x);
        return t("vocabularyPage.intervalRange", { from: a, to: b === undefined ? "—" : t("vocabularyPage.days", { count: b }) });
      },
    },
    {
      title: t("vocabularyPage.nextReview"),
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
              { label: t("vocabularyPage.today"), value: "today" },
              { label: t("vocabularyPage.thisWeek"), value: "week" },
              { label: t("vocabularyPage.thisMonth"), value: "month" },
            ]}
          />
          <Button href={reviewLogsExportUrl()}>{t("vocabularyPage.export")}</Button>
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
          <Statistic title={t("vocabularyPage.answerCount")} value={visibleLogs.length} />
          <Statistic title={t("vocabularyPage.correct")} value={correct} />
          <Statistic title={t("vocabularyPage.incorrect")} value={visibleLogs.length - correct} />
          <Statistic
            title={t("vocabularyPage.accuracy")}
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
          locale={{ emptyText: <Empty description={t("vocabularyPage.emptyHistory")} /> }}
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
              {tab === "words" ? t("vocabularyPage.title") : t("vocabularyPage.history")}
            </Typography.Title>
            <Typography.Text type="secondary">
              {tab === "words"
                ? t("vocabularyPage.titleDescription")
                : t("vocabularyPage.historyDescription")}
            </Typography.Text>
          </div>
          <Button
            icon={<SettingOutlined />}
            href="/settings?section=external_apps"
          >
            {t("vocabularyPage.settings")}
          </Button>
        </div>
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={[
            { key: "words", label: t("vocabularyPage.title"), children: wordsView },
            { key: "history", label: t("vocabularyPage.history"), children: historyView },
          ]}
        />
      </Space>
      <Modal
        open={newWordOpen}
        title={t("vocabularyPage.newWord")}
        onCancel={() => setNewWordOpen(false)}
        onOk={() => void createWord()}
      >
        <Form form={newWordForm} layout="vertical">
          <Form.Item name="term" label={t("vocabularyPage.word")} rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="meaning" label={t("vocabularyPage.meaning")} rules={[{ required: true }]}>
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="part_of_speech" label={t("vocabularyPage.partOfSpeech")}>
            <Input />
          </Form.Item>
          <Form.Item name="sentence" label={t("vocabularyPage.sentence")}>
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="translation" label={t("vocabularyPage.translation")}>
            <Input.TextArea />
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        className="vocabulary-review-modal"
        open={!!question}
        title={
          <span>
            {t("vocabularyPage.review")}{" "}
            <Typography.Text type="secondary">
              {reviewQueue.length ? t("vocabularyPage.pendingQuestions", { count: reviewQueue.length }) : ""}
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
                  placeholder={t("vocabularyPage.typeAnswer")}
                  onChange={(event: ChangeEvent<HTMLInputElement>) => setObjectiveAnswer(event.target.value)}
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
                  {t("vocabularyPage.submitAnswer")}
                </Button>
                <Typography.Text type="secondary">
                  {t("vocabularyPage.spellingHint")}
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
                            again: t("vocabularyPage.again"),
                            hard: t("vocabularyPage.hard"),
                            good: t("vocabularyPage.remembered"),
                            easy: t("vocabularyPage.easy"),
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
                {t("vocabularyPage.showAnswer")}
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
            <Typography.Title level={2}>{t("vocabularyPage.reviewComplete")}</Typography.Title>
            <Typography.Text type="secondary">
              {t("vocabularyPage.reviewSaved")}
            </Typography.Text>
            <div className="report-score">
              <Progress
                type="circle"
                percent={Math.round((reviewReport.accuracy || 0) * 100)}
                size={112}
              />
              <div>
                <Statistic
                  title={t("vocabularyPage.completedQuestions")}
                  value={reviewReport.total || 0}
                  suffix={t("vocabularyPage.questionUnit")}
                />
                <Typography.Text type="secondary">
                  {t("vocabularyPage.averageInterval", {
                    from: (reviewReport.average_interval_before_days || 0).toFixed(1),
                    to: (reviewReport.average_interval_after_days || 0).toFixed(1),
                  })}
                </Typography.Text>
              </div>
            </div>
            <div className="report-breakdown">
              <span>
                <CheckCircleFilled /> {t("vocabularyPage.rememberedCount", { count: reviewReport.correct || 0 })}
              </span>
              <span>{t("vocabularyPage.practiceCount", { count: reviewReport.incorrect || 0 })}</span>
            </div>
            {reviewReport.difficult_words?.length ? (
              <Alert
                type="warning"
                showIcon
                message={t("vocabularyPage.practiceSuggestion", { words: reviewReport.difficult_words.join(", ") })}
              />
            ) : null}
            <Button
              block
              size="large"
              type="primary"
              onClick={() => setReviewReport(null)}
            >
              {t("vocabularyPage.done")}
            </Button>
          </div>
        ) : null}
      </Modal>
      <Drawer
        open={!!detail}
        title={detail?.word.term}
        onClose={() => setDetail(null)}
        extra={<Button onClick={edit}>{t("vocabularyPage.edit")}</Button>}
      >
        <Descriptions
          column={1}
          items={[
            {
              key: "meaning",
              label: t("vocabularyPage.meaning"),
              children: detail?.word.meaning || "—",
            },
            {
              key: "definition",
              label: t("vocabularyPage.definition"),
              children: detail?.word.definition || "—",
            },
            {
              key: "example",
              label: t("vocabularyPage.sentence"),
              children: detail?.example?.sentence || "—",
            },
            {
              key: "translation",
              label: t("vocabularyPage.exampleTranslation"),
              children: detail?.example?.translation || "—",
            },
            {
              key: "source",
              label: t("vocabularyPage.contentSource"),
              children: detail?.word.source_name || detail?.word.origin_type,
            },
            {
              key: "state",
              label: t("vocabularyPage.reviewStatus"),
              children: detail ? labels[detail.state] || detail.state : "",
            },
          ]}
        />
      </Drawer>
      <Modal
        open={editOpen}
        title={t("vocabularyPage.editWord")}
        onCancel={() => setEditOpen(false)}
        onOk={() => void saveEdit()}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="term" label={t("vocabularyPage.word")} rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="phonetic" label={t("vocabularyPage.phonetic")}>
            <Input />
          </Form.Item>
          <Form.Item name="part_of_speech" label={t("vocabularyPage.partOfSpeech")}>
            <Input />
          </Form.Item>
          <Form.Item name="meaning" label={t("vocabularyPage.basicMeaning")}>
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="definition" label={t("vocabularyPage.definition")}>
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="user_note" label={t("vocabularyPage.note")}>
            <Input.TextArea />
          </Form.Item>
          <Form.Item name="wordbook_ids" label={t("vocabularyPage.wordbook")}>
            <Select
              mode="multiple"
              options={books.map((b) => ({ value: b.id, label: b.name }))}
            />
          </Form.Item>
          <Form.Item name="tags" label={t("vocabularyPage.tags")}>
            <Select mode="tags" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
