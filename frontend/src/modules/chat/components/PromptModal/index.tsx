import { forwardRef, useImperativeHandle, useState, useEffect, useMemo } from "react";
import { Modal, Button, Input, Divider, Empty, Form, message, Tag } from "antd";
import { useTranslation } from "react-i18next";
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  PushpinFilled,
  PushpinOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { PromptServiceApi } from "@/modules/chat/utils/request";
import "./index.scss";
import {
  Prompt,
  PromptServiceApiPromptServiceUpdatePromptRequest,
  PromptServiceApiPromptServiceCreatePromptRequest,
} from "@/api/generated/chatbot-client";

interface ForwardProps {
  onSelectPrompt: (prompt: string) => void;
}

type updateParams =
  | PromptServiceApiPromptServiceUpdatePromptRequest
  | PromptServiceApiPromptServiceCreatePromptRequest;

export interface PromptImperativeProps {
  onOpen: () => void;
}

const { TextArea } = Input;

const PRESET_PROMPT_IDS = ["preset-1", "preset-2", "preset-3"] as const;
type PresetPrompt = {
  id: string;
  display_name: string;
  content: string;
  isPreset: true;
};
type PromptListItem = Prompt | PresetPrompt;

function isPresetPrompt(prompt: PromptListItem): prompt is PresetPrompt {
  return "isPreset" in prompt && prompt.isPreset === true;
};

const PromptModal = forwardRef<PromptImperativeProps, ForwardProps>(
  ({ onSelectPrompt }, ref) => {
    const { t } = useTranslation();

    const presetPrompts = useMemo(
      () =>
        PRESET_PROMPT_IDS.map((id, index) => ({
          id,
          display_name: t(`chat.presetPrompt${index + 1}Name`),
          content: t(`chat.presetPrompt${index + 1}Content`),
          isPreset: true as const,
        })),
      [t],
    );
    const [visible, setVisible] = useState(false);
    const [addModalVisible, setAddModalVisible] = useState(false);
    const [isEdit, setIsEdit] = useState(false);
    const [editPromptId, setEditPromptId] = useState<string | undefined>("");
    const [searchKeyword, setSearchKeyword] = useState("");

    const [form] = Form.useForm();

    const [promptList, setPromptList] = useState<Prompt[]>([]);

    useEffect(() => {
      fetchPromptList();
    }, []);

    useImperativeHandle(ref, () => ({
      onOpen,
    }));

    function fetchPromptList(keyword = searchKeyword) {
      PromptServiceApi()
        .promptServiceListPrompts({
          pageSize: 9999,
          keyword: keyword.trim() || undefined,
        })
        .then((res) => {
          setPromptList(res.data.prompts ? [...res.data?.prompts] : []);
        });
    }

    function onOpen() {
      setVisible(true);
      setSearchKeyword("");
      fetchPromptList("");
    }

    function showAddPromptModal(prompt?: Prompt) {
      form.setFieldsValue({
        display_name: prompt ? prompt.display_name : "",
        content: prompt ? prompt.content : "",
      });
      setIsEdit(!!prompt);
      setEditPromptId(prompt?.id);
      setAddModalVisible(true);
    }

    function deletePrompt(id: string) {
      PromptServiceApi()
        .promptServiceDeletePrompt({ prompt: id })
        .then(() => {
          message.success(t("chat.deletePromptSuccess"));
          fetchPromptList();
        });
    }

    function selectPrompt(content: string) {
      setVisible(false);
      onSelectPrompt(content);
    }

    function onAddModalClose() {
      setAddModalVisible(false);
    }

    function onAddModalSave() {
      form.validateFields().then((values: Prompt) => {
        const data: updateParams = isEdit
          ? {
              prompt: editPromptId || "",
              prompt2: values,
            }
          : {
              prompt: values,
            };
        const API = isEdit
          ? PromptServiceApi().promptServiceUpdatePrompt
          : PromptServiceApi().promptServiceCreatePrompt;
        API(data as any).then(() => {
          message.success(
            t("chat.createPromptSuccess", {
              action: isEdit ? t("common.edit") : t("chat.newTemplate"),
            }),
          );
          onAddModalClose();
          fetchPromptList();
        });
      });
    }

    function setDefaultPromptFn(item: Prompt) {
      if (item.is_default) {
        PromptServiceApi()
          .promptServiceUnsetDefaultPrompt({
            prompt: item?.id ?? "",
            unsetDefaultPromptRequest: {
              name: "",
            },
          })
          .then(() => {
            fetchPromptList();
          });
        return;
      }
      PromptServiceApi()
        .promptServiceSetDefaultPrompt({
          prompt: item?.id ?? "",
          setDefaultPromptRequest: {
            name: "",
          },
        })
        .then(() => {
          fetchPromptList();
        });
    }

    function renderDefaultItem(
      item: Prompt,
      isSelected: boolean,
      isDefault: boolean,
    ) {
      if (isSelected) {
        if (isDefault) {
          return (
            <PushpinFilled
              style={{ color: "#1890ff" }}
              onClick={(e) => {
                e.stopPropagation();
                setDefaultPromptFn(item);
              }}
            />
          );
        }
        return (
          <PushpinOutlined
            className="cancelDefaultDataset prompt-actions"
            onClick={(e) => {
              e.stopPropagation();
              setDefaultPromptFn(item);
            }}
          />
        );
      }
      return null;
    }

    const filteredPresetPrompts = useMemo(() => {
      const normalizedKeyword = searchKeyword.trim().toLowerCase();
      if (!normalizedKeyword) {
        return presetPrompts;
      }
      return presetPrompts.filter((prompt) =>
        [prompt.display_name, prompt.content]
          .join("\n")
          .toLowerCase()
          .includes(normalizedKeyword),
      );
    }, [presetPrompts, searchKeyword]);

    const mergedPromptList: PromptListItem[] = useMemo(
      () => [...promptList, ...filteredPresetPrompts],
      [filteredPresetPrompts, promptList],
    );

    const renderPromptList = () => (
      <div className="prompt-list-content">
        <Input
          allowClear
          className="prompt-search"
          placeholder={t("chat.searchPromptPlaceholder")}
          prefix={<SearchOutlined />}
          value={searchKeyword}
          onChange={(event) => {
            const nextKeyword = event.target.value;
            setSearchKeyword(nextKeyword);
            fetchPromptList(nextKeyword);
          }}
        />
        <div className="prompt-add-card" onClick={() => showAddPromptModal()}>
          <PlusOutlined className="prompt-add-icon" />
          <span className="prompt-add-text">{t("chat.newTemplate")}</span>
        </div>
        <div className="prompt-list">
          {mergedPromptList.length === 0 ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t("chat.noPromptMatched")}
              className="prompt-empty"
            />
          ) : null}
          {mergedPromptList.map((prompt, index) => (
            <div key={prompt.id} className="prompt-item">
              <div className="prompt-title">
                <div className="prompt-name">
                  <span className="prompt-index">{index + 1}</span>
                  <span className="prompt-name-text">
                    {prompt.display_name}
                  </span>
                  {isPresetPrompt(prompt) ? (
                    <Tag color="geekblue">{t("chat.preset")}</Tag>
                  ) : (
                    renderDefaultItem(prompt, true, prompt.is_default ?? false)
                  )}
                </div>
                <div className="prompt-actions">
                  {isPresetPrompt(prompt) ? null : (
                    <>
                      <EditOutlined
                        className="clickable-icon"
                        onClick={() => showAddPromptModal(prompt)}
                      />
                      <DeleteOutlined
                        className="clickable-icon"
                        onClick={() => deletePrompt(prompt.id)}
                      />
                    </>
                  )}
                  <Button
                    type="link"
                    onClick={() => selectPrompt(prompt.content)}
                    style={{ padding: 0 }}
                  >
                    {t("chat.use")}
                  </Button>
                </div>
              </div>
              <div style={{ height: "10px" }}></div>
              <span className="prompt-content">{prompt.content}</span>
              <Divider style={{ margin: "10px 0" }} />
            </div>
          ))}
        </div>
      </div>
    );

    return (
      <>
        <Modal
          title={t("chat.promptTemplateTitle")}
          className="prompt-modal"
          width="clamp(320px, 62vw, 624px)"
          centered
          open={visible}
          maskClosable
          closable
          onCancel={() => setVisible(false)}
          footer={[
            <Button key="cancel" onClick={() => setVisible(false)}>
              {t("common.cancel")}
            </Button>,
          ]}
        >
          <div className="prompt-modal-container">
            {renderPromptList()}
          </div>
        </Modal>
        <Modal
          title={isEdit ? t("chat.editPromptTemplate") : t("chat.addPromptTemplate")}
          className="prompt-edit-modal"
          width="clamp(320px, 48vw, 520px)"
          centered
          open={addModalVisible}
          maskClosable={false}
          closable
          okText={t("common.save")}
          onCancel={onAddModalClose}
          onOk={onAddModalSave}
        >
          <Form form={form}>
            <Form.Item
              name="display_name"
              label={t("chat.promptTitle")}
              rules={[{ required: true, message: t("chat.enterPromptTitle") }]}
            >
              <Input
                placeholder={t("chat.enterPromptTitle")}
                showCount
                maxLength={100}
              />
            </Form.Item>
            <Form.Item
              name="content"
              label={t("chat.promptContent")}
              rules={[{ required: true, message: t("chat.enterPromptContent") }]}
            >
              <TextArea
                placeholder={t("chat.enterPromptContent")}
                rows={5}
                showCount
                maxLength={800}
                style={{
                  width: "100%",
                  height: "132px",
                  resize: "none",
                }}
              />
            </Form.Item>
          </Form>
        </Modal>
      </>
    );
  },
);

PromptModal.displayName = "PromptModal";

export default PromptModal;
