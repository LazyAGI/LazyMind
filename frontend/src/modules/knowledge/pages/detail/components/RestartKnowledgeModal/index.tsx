import { Ref, forwardRef, useImperativeHandle, useState } from "react";
import { Modal, Form, message, TreeSelect, Select, Popover } from "antd";
import { QuestionCircleOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import "./index.scss";
import type { ParserConfig } from "@/api/generated/knowledge-client";
import { TaskServiceApi, type StartTaskResult } from "@/modules/knowledge/utils/request";

export const DOC_SUMMARY_GROUP = "doc-summary";

interface IData {
  dataset: string;
  ids: string[];
  names?: string[];
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
const documentSegmentValues = ["block", "line", DOC_SUMMARY_GROUP];

export const REPARSE_SCOPE_SLICE_MISSING = "slice_missing";
export const REPARSE_SCOPE_SLICE_AND_EMBED = "slice_and_embed";
export const REPARSE_SCOPE_REBUILD = "rebuild";

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
    form.setFieldsValue({
      reparse_groups: [],
      reparse_scope: REPARSE_SCOPE_REBUILD,
    });
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
      const { reparse_groups, reparse_scope } = (await form.validateFields()) || {};
      const normalizedReparseGroups = normalizeReparseGroups(
        reparse_groups || [],
        (parsers || [])
          .map((parser) => parser.name)
          .filter((name): name is string => !!name),
      );
      const scope = reparse_scope || REPARSE_SCOPE_REBUILD;
      const isFullRebuild =
        normalizedReparseGroups.includes(allSegmentValue) &&
        scope === REPARSE_SCOPE_REBUILD;
      const reparseGroups = isFullRebuild
        ? []
        : expandReparseGroupsForSubmit(normalizedReparseGroups).filter(
            (v: string) => !allParseList.includes(v),
          );
      if (!isFullRebuild && !reparseGroups.length) {
        message.error(t("knowledge.selectReparseTarget"));
        return;
      }

      const docNames = (modalInfo.names || []).filter(Boolean);
      const displayName =
        docNames.length === 1
          ? t("knowledge.reparseTaskNameSingle", { name: docNames[0] })
          : docNames.length > 1
            ? t("knowledge.reparseTaskNameMulti", { name: docNames[0], count: ids.length })
            : t("knowledge.reparseTaskName", { count: ids.length });

      const createRes = await TaskServiceApi().createTasks(dataset, {
        parent: `datasets/${dataset}`,
        items: [
          {
            upload_file_id: "",
            task: {
              task_type: "TASK_TYPE_REPARSE",
              document_ids: ids.filter((i: string) => !!i),
              display_name: displayName,
              reparse_groups: reparseGroups,
              reparse_mode: scope,
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

  const scopeOptions = [
    {
      value: REPARSE_SCOPE_SLICE_MISSING,
      label: t("knowledge.reparseStrategyFillGaps"),
    },
    {
      value: REPARSE_SCOPE_SLICE_AND_EMBED,
      label: t("knowledge.reparseStrategyRebuildVectors"),
    },
    {
      value: REPARSE_SCOPE_REBUILD,
      label: t("knowledge.reparseStrategyFullReparse"),
    },
  ];

  const reparseStrategyLabel = (
    <span className="reparse-strategy-label">
      {t("knowledge.reparseStrategy")}
      <Popover
        trigger={["hover", "click"]}
        placement="rightTop"
        overlayClassName="reparse-strategy-popover"
        styles={{
          body: {
            maxWidth: 360,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          },
        }}
        content={
          <div className="reparse-strategy-help">
            {t("knowledge.reparseStrategyHelp")}
          </div>
        }
      >
        <span className="reparse-strategy-help-trigger">
          <QuestionCircleOutlined className="reparse-strategy-help-icon" />
        </span>
      </Popover>
    </span>
  );

  return (
    <Modal
      open={visible}
      destroyOnHidden
      title={modalInfo?.title}
      centered
      onCancel={onCancel}
      onOk={onOk}
      width={459}
      okButtonProps={{ disabled: loading }}
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="reparse_groups"
          label={t("knowledge.reparseTarget")}
          rules={[{ required: true, message: t("knowledge.selectReparseTarget") }]}
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
            treeData={formatOptions(t)}
          />
        </Form.Item>
        <Form.Item
          name="reparse_scope"
          label={reparseStrategyLabel}
          rules={[{ required: true, message: t("knowledge.selectReparseStrategy") }]}
        >
          <Select options={scopeOptions} />
        </Form.Item>
      </Form>
    </Modal>
  );
};

function expandReparseGroupsForSubmit(groups: string[]) {
  if (groups.includes(allSegmentValue)) {
    return [...documentSegmentValues];
  }
  return groups;
}

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

function formatOptions(t: (key: string, options?: any) => string) {
  const segmentLabel: Record<string, string> = {
    block: t("knowledge.segmentBlock"),
    line: t("knowledge.segmentLine"),
    [DOC_SUMMARY_GROUP]: t("knowledge.segmentSummaryShort"),
  };
  const documentChild = documentSegmentValues.map((name) => ({
    title: segmentLabel[name] || name,
    value: name,
  }));

  return [
    { title: t("knowledge.segmentAll"), value: allSegmentValue },
    {
      title: t("knowledge.segmentDocument"),
      value: documentSegmentValue,
      disabled: true,
      children: documentChild,
    },
  ];
}

export default forwardRef(RestartKnowledgeModal);
