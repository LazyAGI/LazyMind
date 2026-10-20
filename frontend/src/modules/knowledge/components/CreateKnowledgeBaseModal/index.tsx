import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";
import { Modal, Form, Input, Select, Tabs, Typography, Button, Collapse, Tooltip, message } from "antd";
import { QuestionCircleOutlined, SettingOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { Dataset, Algo } from "@/api/generated/knowledge-client";
import type { Dataset as CoreDataset } from "@/api/generated/core-client";
import { KnowledgeBaseServiceApi, getKnowledgeOptions } from "@/modules/knowledge/utils/request";
import {
  KNOWLEDGE_BASE_NAME_MAX_LENGTH,
  KNOWLEDGE_BASE_NAME_PATTERN,
} from "@/modules/knowledge/constants/validation";
import DataSourceProviderPicker from "@/modules/dataSource/components/management/DataSourceProviderPicker";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import TagSelect from "../TagSelect";
import { isDeveloperModeActive } from "@/utils/developerMode";
import {
  PROCESSING_LEVEL_ORDER,
} from "@/modules/knowledge/utils/processingLevel";
import "@/modules/dataSource/index.scss";
import "./index.scss";
import { createCapabilityProfile, type CapabilityRef, type CustomCapabilityProfile, type LearningCatalog } from "@/modules/learning/api";
import CapabilitySettings, { capabilityRefsFromForm } from "@/modules/learning/CapabilitySettings";

const { TextArea } = Input;
const { Paragraph } = Typography;
const KNOWLEDGE_TAG_MAX_LENGTH = 20;
const CREATE_MODAL_WIDTH = 720;

type CreateTab = "direct" | "cloud";

export interface CreateKnowledgeBaseModalProps {
  onCreate: (dataset: Dataset) => Promise<CoreDataset | void>;
  syncCreateVm: SyncKnowledgeBaseCreationVm;
  embeddingReady?: boolean | null;
}

export interface CreateKnowledgeBaseModalRef {
  onOpen: (tab?: CreateTab) => void;
  onClose: () => void;
}

const CreateKnowledgeBaseModal = forwardRef<
  CreateKnowledgeBaseModalRef,
  CreateKnowledgeBaseModalProps
>(({ onCreate, syncCreateVm }, ref) => {
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);
  const [activeTab, setActiveTab] = useState<CreateTab>("direct");
  const [tags, setTags] = useState<string[]>([]);
  const [algorithm, setAlgorithm] = useState<Algo[]>([]);
  const [hasTagLengthError, setHasTagLengthError] = useState(false);
  const [learningCatalog, setLearningCatalog] = useState<LearningCatalog>();
  const [processingLevels, setProcessingLevels] = useState<string[]>([]);
  const [customProfiles,setCustomProfiles]=useState<CustomCapabilityProfile[]>([]);
  const [form] = Form.useForm();
  const pendingCloudTabRestoreRef = useRef(false);
  const developerModeActive = isDeveloperModeActive();

  useImperativeHandle(ref, () => ({
    onOpen,
    onClose: onCancel,
  }));

  useEffect(() => {
    if (!visible || algorithm.length !== 1) {
      return;
    }
    const currentAlgoId = form.getFieldValue("algo_id");
    if (!currentAlgoId) {
      form.setFieldsValue({ algo_id: algorithm[0].algo_id });
    }
  }, [algorithm, developerModeActive, visible, form]);

  useEffect(() => {
    if (!visible) {
      return;
    }
    const subFlowActive =
      syncCreateVm.wizardOpen ||
      syncCreateVm.authSelectModalOpen ||
      syncCreateVm.feishuSetupModalOpen;
    if (subFlowActive) {
      if (syncCreateVm.wizardOpen) {
        pendingCloudTabRestoreRef.current = false;
      } else {
        pendingCloudTabRestoreRef.current = true;
      }
      setVisible(false);
    }
  }, [
    visible,
    syncCreateVm.wizardOpen,
    syncCreateVm.authSelectModalOpen,
    syncCreateVm.feishuSetupModalOpen,
  ]);

  useEffect(() => {
    const subFlowActive =
      syncCreateVm.wizardOpen ||
      syncCreateVm.authSelectModalOpen ||
      syncCreateVm.feishuSetupModalOpen;
    if (!subFlowActive && pendingCloudTabRestoreRef.current) {
      pendingCloudTabRestoreRef.current = false;
      setActiveTab("cloud");
      setVisible(true);
    }
  }, [
    syncCreateVm.wizardOpen,
    syncCreateVm.authSelectModalOpen,
    syncCreateVm.feishuSetupModalOpen,
  ]);

  function loadFormData() {
    const optionsRequest = getKnowledgeOptions().then(options => {
      setLearningCatalog({ capabilities: options.capabilities, profiles: options.scenes,
        question_types: [], local_available: options.local_available });
      setCustomProfiles(options.profiles);
      setAlgorithm(options.algorithms);
      setProcessingLevels(options.processing_levels);
      form.setFieldsValue({
        learning_profile_key: options.default_scene,
        learning_capability_keys: options.scenes.find(item => item.key === options.default_scene)?.capabilities || [],
        processing_level: options.default_processing_level,
        algo_id: options.default_algo_id,
      });
    });
    const tagsRequest = KnowledgeBaseServiceApi().datasetServiceAllDatasetTags().then(res => setTags(res.data.tags || []));
    return Promise.all([optionsRequest, tagsRequest]);
  }

  function onOpen(tab: CreateTab = "direct") {
    setActiveTab(tab);
    setHasTagLengthError(false);
    form.resetFields();
    loadFormData().then(() => {
      setVisible(true);
    }).catch((error) => {
      console.error("Failed to load knowledge base creation form:", error);
    });
  }

  function onCancel() {
    pendingCloudTabRestoreRef.current = false;
    form.resetFields();
    setHasTagLengthError(false);
    setActiveTab("direct");
    setVisible(false);
  }

  function onSceneChange(key: string) {
    const builtin = learningCatalog?.profiles.find(item => item.key === key);
    if (builtin) {
      form.setFieldsValue({
        learning_capability_keys: builtin.capabilities,
        learning_capability_settings: {},
      });
      return;
    }
    const preset = customProfiles.find(item => item.id === key);
    if (!preset) return;
    try {
      const refs: CapabilityRef[] = JSON.parse(preset.capability_refs_json);
      form.setFieldsValue({
        learning_capability_keys: refs.filter(item => item.enabled).map(item => item.key),
        learning_capability_settings: Object.fromEntries(refs.map(item => [item.key, item.settings || {}])),
      });
    } catch (error) {
      console.error("Invalid capability preset:", error);
      form.setFieldsValue({ learning_capability_keys: [], learning_capability_settings: {} });
    }
  }

  const handleTagLengthErrorChange = useCallback(
    (hasError: boolean) => {
      setHasTagLengthError(hasError);
      form.setFields([
        {
          name: "tags",
          errors: hasError ? [t("knowledge.knowledgeTagMaxLength")] : [],
        },
      ]);
    },
    [form, t],
  );

  function onOk() {
    if (activeTab !== "direct") {
      return;
    }

    if (hasTagLengthError) {
      form.setFields([
        { name: "tags", errors: [t("knowledge.knowledgeTagMaxLength")] },
      ]);
      return;
    }

    form.validateFields().then(async (values) => {
      // Preset fields may not be mounted inside the developer-only Collapse.
      const params = { ...form.getFieldsValue(true), ...values };
      const selectedAlgoId =
        params.algo_id ||
        (algorithm.length === 1 ? algorithm[0]?.algo_id : undefined);
      params.algo = algorithm.find((item) => item.algo_id === selectedAlgoId);
      if (selectedAlgoId) {
        params.algo_id = selectedAlgoId;
      }
      delete params.algo_id;

      if (loading) {
        return;
      }

      setLoading(true);
      try {
        const capabilityKeys = params.learning_capability_keys || [];
        if (!capabilityKeys.length) {
          message.error(t("learning.selectCapabilities"));
          return;
        }
        const capabilitySettings = params.learning_capability_settings || {};
        const profileKey = params.learning_profile_key;
        const customProfileName = params.learning_profile_name;
        delete params.learning_capability_keys;
        delete params.learning_capability_settings;
        delete params.learning_profile_key;
        delete params.learning_profile_name;
        if (profileKey === "custom" && customProfileName) {
          const profile = await createCapabilityProfile(customProfileName, "", capabilityKeys);
          params.profile_id = profile.id;
        }
        params.scene = learningCatalog?.profiles.some(item => item.key === profileKey) ? profileKey : "custom";
        if (customProfiles.some(item => item.id === profileKey)) params.profile_id = profileKey;
        params.reading_capabilities = capabilityRefsFromForm(capabilityKeys, capabilitySettings, learningCatalog?.capabilities);
        await onCreate(params);
        message.success(t("knowledge.createSuccess"));
        onCancel();
      } catch (error) {
        console.error("Create knowledge base error: ", error);
      } finally {
        setLoading(false);
      }
    });
  }

  return (
    <Modal
      open={visible}
      title={t("knowledge.createKnowledgeBase")}
      centered
      destroyOnHidden
      width={CREATE_MODAL_WIDTH}
      className="knowledge-create-modal"
      footer={
        <div className="knowledge-create-modal-footer">
          {activeTab === "direct" ? (
            <>
              <Button onClick={onCancel}>{t("common.cancel")}</Button>
              <Button type="primary" loading={loading} onClick={onOk}>
                {t("common.confirm")}
              </Button>
            </>
          ) : (
            <span className="knowledge-create-modal-footer-spacer" aria-hidden="true" />
          )}
        </div>
      }
      onCancel={onCancel}
    >
      <Tabs
        activeKey={activeTab}
        className="knowledge-create-modal-tabs"
        items={[
          {
            key: "direct",
            label: t("knowledge.createDirect"),
            children: (
              <Form
                form={form}
                layout="horizontal"
                labelCol={{ flex: "116px" }}
                wrapperCol={{ flex: 1 }}
                labelAlign="left"
                colon={false}
                requiredMark={(label, { required }) => (
                  <span className="knowledge-create-label">
                    {label}
                    {required && <span className="knowledge-create-required-mark">*</span>}
                  </span>
                )}
                className="knowledge-create-form"
              >
                <Form.Item
                  name="display_name"
                  label={t("knowledge.knowledgeBaseName")}
                  required
                  rules={[
                    {
                      required: true,
                      message: t("knowledge.inputKnowledgeBaseName"),
                    },
                    {
                      pattern: KNOWLEDGE_BASE_NAME_PATTERN,
                      message: t("knowledge.knowledgeNameRule"),
                    },
                  ]}
                >
                  <Input
                    placeholder={t("knowledge.knowledgeNameRule")}
                    maxLength={KNOWLEDGE_BASE_NAME_MAX_LENGTH}
                  />
                </Form.Item>
                <Form.Item name="desc" label={t("knowledge.knowledgeDesc")}>
                  <TextArea
                    placeholder={t("knowledge.maxLength300Chars")}
                    showCount
                    maxLength={300}
                    autoSize={{ minRows: 2, maxRows: 3 }}
                  />
                </Form.Item>
                <Form.Item
                  name="tags"
                  label={t("knowledge.knowledgeTags")}
                  rules={[
                    {
                      required: true,
                      message: t("knowledge.selectKnowledgeTags"),
                    },
                    {
                      validator: (_, value?: string[]) => {
                        const hasOverLengthTag = (value || []).some(
                          (tag) =>
                            Array.from(tag).length > KNOWLEDGE_TAG_MAX_LENGTH,
                        );
                        return hasOverLengthTag
                          ? Promise.reject(
                              new Error(t("knowledge.knowledgeTagMaxLength")),
                            )
                          : Promise.resolve();
                      },
                    },
                  ]}
                  validateStatus={hasTagLengthError ? "error" : undefined}
                  help={
                    hasTagLengthError
                      ? t("knowledge.knowledgeTagMaxLength")
                      : undefined
                  }
                >
                  <TagSelect
                    tags={tags}
                    maxTagLength={KNOWLEDGE_TAG_MAX_LENGTH}
                    maxTagLengthMessage={t("knowledge.knowledgeTagMaxLength")}
                    showOverLengthInputError
                    onLengthErrorChange={handleTagLengthErrorChange}
                  />
                </Form.Item>
                <Form.Item name="learning_profile_key" label={t("learning.capabilityProfile")}>
                  <Select options={[...(learningCatalog?.profiles || []).map((profile) => ({ value:profile.key, label:t(profile.name_i18n_key) })),...customProfiles.map(profile=>({value:profile.id,label:profile.custom_name})), ...(developerModeActive ? [{value:"custom",label:t("learning.customProfile")}] : [])]}
                    onChange={onSceneChange} />
                </Form.Item>
                <Form.Item noStyle shouldUpdate={(a,b)=>a.learning_profile_key!==b.learning_profile_key}>{({getFieldValue})=>getFieldValue("learning_profile_key")==="custom"?<Form.Item name="learning_profile_name" label={t("learning.customProfileName")} ><Input/></Form.Item>:null}</Form.Item>
                {developerModeActive && <Collapse
                  className="knowledge-create-advanced"
                  ghost
                  items={[{
                    key: "advanced",
                    label: <span className="knowledge-create-advanced-title"><SettingOutlined />{t("learning.advancedSettings")}</span>,
                    children: <>
                      <Form.Item
                        name="processing_level"
                        label={<span>{t("knowledge.processingLevel")} <Tooltip title={t("knowledge.processingLevelHint")}><QuestionCircleOutlined className="knowledge-create-help-icon" /></Tooltip></span>}
                      >
                        <Select options={PROCESSING_LEVEL_ORDER.map((level) => ({
                          value: level,
                          label: t(`knowledge.processing${level[0].toUpperCase()}${level.slice(1)}`),
                          disabled: !processingLevels.includes(level),
                        }))} />
                      </Form.Item>
                      {algorithm.length !== 1 && (
                        <Form.Item
                          name="algo_id"
                          label={t("knowledge.parseAlgorithm")}
                          initialValue={null}
                          rules={[{ required: true, message: t("knowledge.selectParseAlgorithm") }]}
                        >
                          <Select
                            options={algorithm.map((item) => ({ label: item.display_name, value: item.algo_id }))}
                            placeholder={t("knowledge.selectParseAlgorithm")}
                          />
                        </Form.Item>
                      )}
                      <Form.Item
                        name="learning_capability_keys"
                        label={<span>{t("learning.knowledgeBaseCapabilities")} <Tooltip title={t("learning.knowledgeBaseCapabilitiesHint")}><QuestionCircleOutlined className="knowledge-create-help-icon" /></Tooltip></span>}
                        rules={[{required:true,message:t("learning.selectCapabilities")}]}
                      >
                        <Select mode="multiple" options={(learningCatalog?.capabilities || []).map((item) => ({value:item.key,label:t(item.name_i18n_key),disabled:learningCatalog?.local_available===false}))} onChange={() => form.setFieldValue("learning_profile_key","custom")} />
                      </Form.Item>
                      <Form.Item noStyle shouldUpdate={(a,b)=>a.learning_capability_keys!==b.learning_capability_keys}>{({getFieldValue})=><CapabilitySettings capabilities={learningCatalog?.capabilities || []} selectedKeys={getFieldValue("learning_capability_keys") || []}/>}</Form.Item>
                    </>,
                  }]}
                />}
              </Form>
            ),
          },
          {
            key: "cloud",
            label: t("knowledge.createFromCloudDisk"),
            children: (
              <div className="knowledge-create-modal-cloud">
                <Paragraph className="data-source-create-provider-intro">
                  {t("knowledge.createFromCloudDocumentsIntro")}
                </Paragraph>
                <DataSourceProviderPicker vm={syncCreateVm} showGoogleDrive />
              </div>
            ),
          },
        ]}
        onChange={(key) => setActiveTab(key as CreateTab)}
      />
    </Modal>
  );
});

CreateKnowledgeBaseModal.displayName = "CreateKnowledgeBaseModal";

export default CreateKnowledgeBaseModal;
