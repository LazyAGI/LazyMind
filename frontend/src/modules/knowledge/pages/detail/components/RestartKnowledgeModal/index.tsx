import { Ref, forwardRef, useImperativeHandle, useState } from "react";
import { Modal, Form, message, TreeSelect } from "antd";
import { useTranslation } from "react-i18next";
import type { ParserConfig } from "@/api/generated/knowledge-client";
import { TaskServiceApi, type StartTaskResult } from "@/modules/knowledge/utils/request";

interface IData {
  dataset: string;
  ids: string[];
  title: string;
}

export interface IRestartKnowledgeProps {
  onOpen: (data: IData) => void;
}

interface IProps {
  parsers?: Array<ParserConfig>;
  onFinish: () => void;
}

const allParseList = ["all", "document"];
const allSegmentValue = "all";
const documentSegmentValue = "document";
const documentSegmentValues = ["line", "block"];

const RestartKnowledgeModal = (
  props: IProps,
  ref: Ref<unknown> | undefined,
) => {
  const { parsers, onFinish } = props;
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);
  const [modalInfo, setModalInfo] = useState<IData>();
  const [form] = Form.useForm();

  useImperativeHandle(ref, () => ({
    onOpen,
  }));

  const onOpen = (data: IData) => {
    setVisible(true);
    setModalInfo(data);
  };

  const onCancel = () => {
    setVisible(false);
    form.resetFields();
  };

  const onOk = async () => {
    if (!modalInfo) {
      return;
    }
    setLoading(true);
    try {
      const { dataset, ids } = modalInfo;
      const { reparse_groups } = (await form.validateFields()) || {};
      const normalizedReparseGroups = normalizeReparseGroups(reparse_groups || []);

      const createRes = await TaskServiceApi().createTasks(dataset, {
        parent: `datasets/${dataset}`,
        items: [
          {
            upload_file_id: "",
            task: {
              task_type: "TASK_TYPE_REPARSE",
              document_ids: ids.filter((i: string) => !!i),
              display_name: t("knowledge.reparseTaskName", { count: ids.length }),
              reparse_groups: normalizedReparseGroups.filter(
                (v: string) => !allParseList.includes(v),
              ),
            },
          },
        ],
      });

      const tasks = createRes.data.tasks || [];
      const taskIds = tasks
        .map((task: { task_id?: string }) => task.task_id)
        .filter((taskId: string | undefined): taskId is string => !!taskId);
      if (!taskIds.length) {
        message.error(t("knowledge.createReparseTaskFailed"));
        return;
      }

      const startRes = await TaskServiceApi().startTasks(dataset, { task_ids: taskIds });
      const startedCount = startRes.data.started_count ?? 0;
      if (startedCount <= 0) {
        const failedTasks = (startRes.data.tasks || []) as StartTaskResult[];
        const errMsg =
          failedTasks.find((task: StartTaskResult) => task.message)?.message ||
          t("knowledge.createReparseTaskFailed");
        message.error(errMsg);
        return;
      }
      message.success(t("knowledge.createReparseTaskSuccess"));
      onFinish?.();
      onCancel();
    } catch (error) {
      console.log(error);
      message.error(t("knowledge.createReparseTaskFailed"));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      open={visible}
      destroyOnHidden
      title={modalInfo?.title}
      centered
      onCancel={onCancel}
      onOk={onOk}
      width={459}
      height={300}
      okButtonProps={{ disabled: loading }}
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="reparse_groups"
          label={t("knowledge.restartSlice")}
          rules={[{ required: true, message: t("knowledge.selectRestartSlice") }]}
          getValueFromEvent={(value: Array<string | undefined>) =>
            normalizeReparseGroups(
              value || [],
              (parsers || [])
                .map((parser) => parser.name)
                .filter((name): name is string => !!name),
            )
          }
          required
        >
          <TreeSelect
            multiple
            treeData={formatOptions(parsers || [], t)}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
};

const parseKeyMap = {
  PARSE_TYPE_QA: "knowledge.segmentQa",
  PARSE_TYPE_SUMMARY: "knowledge.segmentSummary",
  PARSE_TYPE_IMAGE_CAPTION: "knowledge.imageCaption",
} as const;

function normalizeReparseGroups(
  value: Array<string | undefined>,
  extraValues: string[] = [],
) {
  const selectableValues = new Set([
    allSegmentValue,
    ...documentSegmentValues,
    ...extraValues,
  ]);
  const normalizedValue = value.filter(
    (v): v is string => !!v && selectableValues.has(v),
  );
  const hasAllSegment = normalizedValue.includes(allSegmentValue);
  const documentGroupValues = normalizedValue.filter((v) => v !== allSegmentValue);

  if (!hasAllSegment || !documentGroupValues.length) {
    return normalizedValue;
  }

  const latestValue = normalizedValue[normalizedValue.length - 1];
  return latestValue === allSegmentValue ? [allSegmentValue] : documentGroupValues;
}

function formatOptions(parsers: Array<ParserConfig>, t: (key: string, options?: any) => string) {
  if (!parsers || !parsers.length) {
    return [];
  }
  const documentChild: {
    title: string | undefined;
    value: string | undefined;
    disabled?: boolean;
  }[] = [];
  const options = [
    { title: t("knowledge.segmentAll"), value: allSegmentValue },
    { title: t("knowledge.segmentDocument"), value: documentSegmentValue, disabled: true },
  ];

  parsers.forEach((p) => {
    if (p.type === "PARSE_TYPE_SPLIT" && p.name && documentSegmentValues.includes(p.name)) {
      documentChild.push({
        title: p.name,
        value: p.name,
      });
    } else if (p.type && p.type in parseKeyMap) {
      options.push({
        title: t(parseKeyMap[p.type as keyof typeof parseKeyMap]),
        value: p?.name || "",
      });
    }
  });
  if (documentChild.length) {
    (
      options[1] as {
        title: string;
        value: string;
        disabled?: boolean;
        children?: typeof documentChild;
      }
    ).children = documentChild;
  }

  return options;
}

export default forwardRef(RestartKnowledgeModal);
