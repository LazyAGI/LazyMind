import {
  Alert,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Spin,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import type { PdfTextSelection } from "@/components/ui";
import {
  getTranslationStatus,
  translateText,
} from "@/modules/knowledge/api/translation";
import {
  addVocabularyWord,
  lookupDictionary,
  resolveVocabularySelection,
  type DictionaryEntry,
  type SelectionResolveResult,
} from "./api";
import "./AddVocabularyModal.scss";

interface Props {
  selection: PdfTextSelection | null;
  datasetId: string;
  documentId: string;
  segmentId?: string;
  context?: string;
  onClose: () => void;
  onAdded: () => void;
}
const cleanText = (value?: string) =>
  String(value || "")
    .replace(/\\n/g, "\n")
    .trim();
const senseRows = (entry?: DictionaryEntry) => {
  const sense = entry?.senses?.[0];
  const fallback = cleanText(sense?.part_of_speech);
  return cleanText(sense?.translation)
    .split("\n")
    .filter(Boolean)
    .map((line) => {
      const match = line.match(/^([a-z]+(?:\.[a-z]+)*\.?)\s+(.+)$/i);
      return { pos: match?.[1] || fallback, text: match?.[2] || line };
    });
};
const DictionaryComparison = ({
  title,
  phonetic,
  pos,
  meaning,
  definition,
  empty = false,
}: {
  title: string;
  phonetic?: string;
  pos?: string;
  meaning?: string;
  definition?: string;
  empty?: boolean;
}) => (
  <div className="dictionary-comparison-card">
    <Typography.Text type="secondary">{title}</Typography.Text>
    {empty ? (
      <Typography.Text type="secondary">没有匹配的词典结果</Typography.Text>
    ) : (
      <>
        <Space wrap>
          {pos ? <Tag color="blue">{cleanText(pos)}</Tag> : null}
          {phonetic ? <Tag>{cleanText(phonetic)}</Tag> : null}
        </Space>
        <strong>{cleanText(meaning) || "暂无中文释义"}</strong>
        {definition ? (
          <Typography.Paragraph type="secondary" ellipsis={{ rows: 3 }}>
            {cleanText(definition)}
          </Typography.Paragraph>
        ) : null}
      </>
    )}
  </div>
);

export default function AddVocabularyModal({
  selection,
  datasetId,
  documentId,
  segmentId,
  context,
  onClose,
  onAdded,
}: Props) {
  const [form] = Form.useForm();
  const [resolved, setResolved] = useState<SelectionResolveResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [translating, setTranslating] = useState(false);
  const [selectedDictionary, setSelectedDictionary] = useState("");
  const [lookingUp, setLookingUp] = useState(false);
  const [initialSentence, setInitialSentence] = useState("");
  const lookupInFlight = useRef("");
  const currentSentence = Form.useWatch("sentence", form);
  const dictionary = useMemo(() => resolved?.dictionary || [], [resolved]);
  const applyDictionary = (entry?: DictionaryEntry) => {
    const sense = entry?.senses?.[0];
    form.setFieldsValue({
      dictionary_entry_id: entry?.id,
      phonetic: cleanText(entry?.phonetic),
      part_of_speech: cleanText(sense?.part_of_speech),
      meaning: cleanText(sense?.translation),
      definition: cleanText(sense?.definition),
    });
  };
  const translate = async (sentence?: string, silent = false) => {
    const value = sentence || form.getFieldValue("sentence");
    if (!value) return;
    setTranslating(true);
    try {
      const result = await translateText(value);
      form.setFieldValue("translation", result.translated_text);
    } catch {
      if (!silent) message.error("翻译失败，可手工填写后继续保存");
    } finally {
      setTranslating(false);
    }
  };
  useEffect(() => {
    if (!selection) return;
    setLoading(true);
    void resolveVocabularySelection({
      term: selection.text,
      language: "en",
      context: selection.context || context || selection.text,
    })
      .then(async (result) => {
        const normalized = {
          ...result,
          dictionary: result.dictionary || [],
          wordbooks: result.wordbooks || [],
        };
        setResolved(normalized);
        const first = normalized.dictionary[0];
        setSelectedDictionary(first?.id || "");
        const sentence = cleanText(normalized.sentence);
        setInitialSentence(sentence);
        form.setFieldsValue({
          term: normalized.term,
          wordbook_ids: normalized.wordbooks[0]
            ? [normalized.wordbooks[0].id]
            : [],
          sentence,
          tags: [],
          example_tags: [],
        });
        applyDictionary(first);
        try {
          if (sentence && (await getTranslationStatus()))
            await translate(sentence, true);
        } catch {
          /* Translation is an optional capability. */
        }
      })
      .catch((e) => message.error(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  }, [context, form, selection]);
  const chooseDictionary = (id: string) => {
    setSelectedDictionary(id);
    applyDictionary(dictionary.find((x) => x.id === id));
  };
  const refreshDictionary = async () => {
    const term = String(form.getFieldValue("term") || "").trim();
    const key = term.toLocaleLowerCase();
    if (
      !term ||
      key === resolved?.term.toLocaleLowerCase() ||
      lookupInFlight.current === key
    )
      return;
    lookupInFlight.current = key;
    setLookingUp(true);
    try {
      const entries = await lookupDictionary(term);
      const first = entries[0];
      const acknowledge = () =>
        setResolved((current) =>
          current
            ? { ...current, term }
            : {
                term,
                sentence: "",
                provider: "local",
                dictionary: [],
                wordbooks: [],
              },
        );
      const apply = () => {
        setResolved((current) =>
          current
            ? { ...current, term, dictionary: entries }
            : {
                term,
                sentence: "",
                provider: "local",
                dictionary: entries,
                wordbooks: [],
              },
        );
        setSelectedDictionary(first?.id || "");
        applyDictionary(first);
      };
      if (!dictionary.length) apply();
      else {
        const currentValues = form.getFieldsValue([
          "phonetic",
          "part_of_speech",
          "meaning",
          "definition",
        ]);
        Modal.confirm({
          className: "dictionary-comparison-modal",
          width: 720,
          title: "发现目标词已修改",
          content: (
            <>
              <Typography.Paragraph type="secondary">
                对比当前内容与新目标词的词典结果，再决定是否覆盖。
              </Typography.Paragraph>
              <div className="dictionary-comparison">
                <DictionaryComparison
                  title="当前内容"
                  phonetic={currentValues.phonetic}
                  pos={currentValues.part_of_speech}
                  meaning={currentValues.meaning}
                  definition={currentValues.definition}
                />
                <DictionaryComparison
                  title="新词典内容"
                  phonetic={first?.phonetic}
                  pos={first?.senses?.[0]?.part_of_speech}
                  meaning={first?.senses?.[0]?.translation}
                  definition={first?.senses?.[0]?.definition}
                  empty={!first}
                />
              </div>
            </>
          ),
          okText: "使用新内容",
          cancelText: "保留当前内容",
          onOk: apply,
          onCancel: acknowledge,
        });
      }
    } catch {
      lookupInFlight.current = "";
      message.error("词典查询失败，请稍后重试");
    } finally {
      setLookingUp(false);
    }
  };
  const submit = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      const result = await addVocabularyWord({
        ...values,
        provider: undefined,
        dataset_id: datasetId,
        document_id: documentId,
        segment_id: segmentId,
        page: selection?.page,
        bbox: selection?.bbox,
        selected_text: selection?.text,
        context_sentence: values.sentence,
        origin_type: "document",
      });
      message.success(
        result.queued
          ? "Anki 未连接，已加入待处理队列"
          : resolved?.existing
            ? "已补充到现有单词"
            : "已加入生词表",
      );
      onAdded();
      onClose();
    } finally {
      setSaving(false);
    }
  };
  const selectedEntry = dictionary.find((x) => x.id === selectedDictionary);
  const rows = senseRows(selectedEntry);
  return (
    <Modal
      className="add-vocabulary-modal"
      open={Boolean(selection)}
      title={
        <div>
          <div>加入生词</div>
          <Typography.Text type="secondary" className="add-vocabulary-subtitle">
            将该单词加入生词本，便于后续复习和学习。
          </Typography.Text>
        </div>
      }
      width={860}
      onCancel={onClose}
      onOk={() => void submit()}
      confirmLoading={saving}
      destroyOnHidden
      styles={{ body: { maxHeight: "72vh", overflow: "auto" } }}
    >
      {loading ? (
        <div className="add-vocabulary-loading">
          <Spin />
        </div>
      ) : (
        <Form form={form} layout="vertical">
          <div className="add-vocabulary-summary">
            <Form.Item name="term" label="目标词" rules={[{ required: true }]}>
              <Input
                onBlur={() => void refreshDictionary()}
                onPressEnter={(event) => {
                  event.preventDefault();
                  void refreshDictionary();
                }}
                suffix={lookingUp ? <Spin size="small" /> : null}
              />
            </Form.Item>
            <Form.Item
              name="wordbook_ids"
              label="加入单词本"
              rules={[{ required: true, message: "请选择至少一个单词本" }]}
            >
              <Select
                mode="multiple"
                placeholder="选择单词本"
                options={(resolved?.wordbooks || []).map((x) => ({
                  value: x.id,
                  label: x.name,
                }))}
              />
            </Form.Item>
          </div>
          {resolved?.existing ? (
            <Alert
              type="info"
              showIcon
              message="该词已存在，本次会补充单词本、来源和文档例句"
              className="add-vocabulary-alert"
            />
          ) : null}
          <section className="add-vocabulary-section">
            <div className="add-vocabulary-section-title">
              <Typography.Text strong>词义</Typography.Text>
              {dictionary.length ? (
                <Select
                  aria-label="选择词典"
                  value={selectedDictionary}
                  onChange={chooseDictionary}
                  style={{ width: 220 }}
                  options={dictionary.map((entry) => ({
                    value: entry.id,
                    label: entry.source_name,
                  }))}
                />
              ) : null}
            </div>
            {selectedEntry ? (
              <div className="dictionary-detail">
                <Space size={8} wrap>
                  {selectedEntry.phonetic ? (
                    <Tag>{cleanText(selectedEntry.phonetic)}</Tag>
                  ) : null}
                  <Tag
                    color={
                      selectedEntry.source_name === "ECDICT"
                        ? "green"
                        : "default"
                    }
                  >
                    {selectedEntry.source_name}
                  </Tag>
                </Space>
                <div className="dictionary-senses">
                  {rows.map((row, index) => (
                    <div
                      className="dictionary-sense-row"
                      key={`${row.pos}-${index}`}
                    >
                      <span>{row.pos}</span>
                      <strong>{row.text}</strong>
                    </div>
                  ))}
                </div>
                {selectedEntry.senses?.[0]?.definition ? (
                  <Typography.Paragraph
                    type="secondary"
                    ellipsis={{ rows: 3, expandable: true, symbol: "展开" }}
                    className="dictionary-choice-definition"
                  >
                    {cleanText(selectedEntry.senses[0].definition)}
                  </Typography.Paragraph>
                ) : null}
              </div>
            ) : (
              <Alert
                type="warning"
                message="词典未找到可靠的中文释义，请换一个目标词后重试"
              />
            )}
            <Form.Item name="dictionary_entry_id" hidden>
              <Input />
            </Form.Item>
            <Form.Item name="phonetic" hidden>
              <Input />
            </Form.Item>
            <Form.Item name="part_of_speech" hidden>
              <Input />
            </Form.Item>
            <Form.Item name="meaning" hidden>
              <Input />
            </Form.Item>
            <Form.Item name="definition" hidden>
              <Input />
            </Form.Item>
            {selectedEntry?.examples?.length ? (
              <div className="dictionary-examples">
                <Tag color="blue">词典例句</Tag>
                {selectedEntry.examples.map((example) => (
                  <div key={example.sentence}>
                    <Typography.Text>{example.sentence}</Typography.Text>
                    {example.translation ? (
                      <Typography.Text type="secondary">
                        　{example.translation}
                      </Typography.Text>
                    ) : null}
                  </div>
                ))}
              </div>
            ) : null}
          </section>
          <section className="add-vocabulary-section">
            <div className="add-vocabulary-section-title">
              <Space>
                <Typography.Text strong>文档例句</Typography.Text>
                <Tag color="green">来自当前文档</Tag>
              </Space>
              {cleanText(currentSentence) !== initialSentence ? (
                <Button
                  size="small"
                  loading={translating}
                  onClick={() => void translate()}
                >
                  翻译原句
                </Button>
              ) : null}
            </div>
            <div className="add-vocabulary-example-grid">
              <Form.Item
                name="sentence"
                label="原句"
                rules={[{ required: true }]}
              >
                <Input.TextArea autoSize={{ minRows: 2, maxRows: 4 }} />
              </Form.Item>
              <Form.Item name="translation" label="中文译文">
                <Input.TextArea autoSize={{ minRows: 2, maxRows: 4 }} />
              </Form.Item>
            </div>
          </section>
          <div className="add-vocabulary-tags">
            <Form.Item name="tags" label="单词标签">
              <Select mode="tags" tokenSeparators={[",", "，"]} />
            </Form.Item>
            <Form.Item name="example_tags" label="例句标签">
              <Select mode="tags" tokenSeparators={[",", "，"]} />
            </Form.Item>
          </div>
        </Form>
      )}
    </Modal>
  );
}
