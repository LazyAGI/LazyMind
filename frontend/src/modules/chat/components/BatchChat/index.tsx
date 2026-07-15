import { forwardRef, useImperativeHandle, useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Button,
  Empty,
  Flex,
  Form,
  message,
  Modal,
  Select,
  Spin,
  Steps,
  Table,
} from "antd";
import {
  DeleteOutlined,
  InboxOutlined,
  LoadingOutlined,
  PaperClipOutlined,
} from "@ant-design/icons";
import "./index.scss";
import Upload, { RcFile } from "antd/es/upload";
import {
  ChatFileServiceApi,
  ChatServiceApi,
  DatabaseBaseServiceApi,
  KnowledgeBaseServiceApi,
} from "@/modules/chat/utils/request";
import { Dataset, UserDatabaseSummary } from "@/api/generated/knowledge-client";
import {
  BatchChatJob,
  BatchChatJobResultItem,
  SearchKnowledgeConfig,
} from "@/api/generated/chatbot-client";
import { downloadUrl } from "@/modules/chat/utils/download";
import RiskTip from "../RiskTip";
import { localizeErrorCode } from "@/components/request";
const { Dragger } = Upload;

interface ForwardProps {
  cancelFn: (dotBool: string) => void;
}

const initialValues = {
  knowledge: null,
  database: null,
  file: [],
};

export interface BatchChatImperativeProps {
  onOpen: () => void;
}

let timer: ReturnType<typeof setInterval> | undefined;
const BatchChatComponent = forwardRef<BatchChatImperativeProps, ForwardProps>(
  (props, ref) => {
    const { t } = useTranslation();
    const { cancelFn } = props;
    const batchChatTask = localStorage.getItem("batchChatTask");
    const batchChatJobId = localStorage.getItem("batchChatJobId");
    const [visible, setVisible] = useState(false);
    const items = [
      { title: t("chat.importData") },
      { title: t("chat.exportResult") },
    ];
    const [current, setCurrent] = useState(0);
    const templateUrl = new URL("/批量对话模板.xlsx", import.meta.url).href;
    const [form] = Form.useForm();
    const [loading, setLoading] = useState(false);
    const [dataSource, setDataSource] = useState<BatchChatJobResultItem[]>([]);
    const [knowledgeBaseList, setKnowledgeBaseList] = useState<Dataset[]>([]);
    const [databaseBaseList, setDatabaseBaseList] = useState<
      UserDatabaseSummary[]
    >([]);
    const [uploadFile, setUploadFile] = useState<RcFile>();
    const [fileId, setFileId] = useState("");
    const [batchChatTaskResult, setBatchChatTaskResult] =
      useState<BatchChatJob>({} as BatchChatJob);

    function getDatabaseBaseList() {
      // DatabaseBaseServiceApi()
      //   .databaseServiceGetUserDatabaseSummaries({})
      //   .then((res) => {
      //     setDatabaseBaseList((res.data as UserDatabaseSummary[]) || []);
      //   });
      setDatabaseBaseList([]);
    }

    function getKnowledgeBaseList() {
      KnowledgeBaseServiceApi()
        .datasetServiceListDatasets({ pageSize: 1000 })
        .then((res) => {
          setKnowledgeBaseList(res.data.datasets || []);
        });
    }

    function getFileReviewResult(job: string) {
      ChatServiceApi()
        .conversationServicePreviewBatchChatJobResult({ job })
        .then((res) => {
          const data =
            res?.data?.items
              ?.slice(0, 10)
              ?.map((it, i) => ({ ...it, index: i + 1 })) || [];
          setDataSource(data);
        });
    }

    function getFileResult(job: string) {
      ChatServiceApi()
        .conversationServiceGetBatchChatJob({ job })
        .then((res) => {
          const { status } = res.data;
          setBatchChatTaskResult(res.data);
          clearInterval(timer);
          if (status !== "BATCH_CHAT_JOB_STATUS_SUCCESS") {
            timer = setInterval(() => {
              getFileResult(job);
            }, 5000);
          } else {
            getFileReviewResult(job);
            setLoading(false);
          }
        });
    }

    function onFinish(values: any) {
      const { knowledge, database } = values;
      if (!uploadFile?.uid?.length) {
        message.error(t("chat.uploadFileRequired"));
        return;
      }
      ChatServiceApi()
        .conversationServiceBatchChat({
          batchChatRequest: {
            conversation: {
              search_config: {
                dataset_list: knowledge ? [{ id: knowledge }] : [],
                database_ids: database ? [database] : [],
              } as SearchKnowledgeConfig,
            },
            file_id: fileId,
          },
        })
        .then((res) => {
          localStorage.setItem("batchChatJobId", res?.data.job_id || "");
          getFileResult(res?.data.job_id || "");
          setLoading(true);
          setCurrent(1);
        });
    }

    useEffect(() => {
      if (batchChatTask === "true") {
        setCurrent(1);
        setLoading(true);
        getFileResult(batchChatJobId || "");
      }
    }, [batchChatTask]);

    useEffect(() => {
      getKnowledgeBaseList();
      getDatabaseBaseList();
    }, []);

    const columns = [
      {
        title: t("chat.sequence"),
        dataIndex: "index",
        width: 100,
      },
      {
        title: t("chat.question"),
        dataIndex: "question",
      },
      {
        title: t("chat.answer"),
        dataIndex: "answer",
      },
    ];

    useImperativeHandle(ref, () => ({
      onOpen: () => {
        setVisible(true);
      },
    }));

    function uploadFileChange(file: RcFile) {
      setUploadFile(file);
      ChatFileServiceApi()
        .fileServicePresignAttachment({
          presignAttachmentRequest: {
            file: file.name,
            file_size: file.size,
          },
        })
        .then((res) => {
          const { file_id, uri } = res.data;
          setFileId(file_id || "");
          fetch(uri!, {
            method: "PUT",
            body: file,
          })
            .then((response) => {
              if (!response.ok) {
                throw new Error(localizeErrorCode('2000509'));
              }
            })
            .catch((err) => {
              console.error('File upload failed:', err);
              setFileId('');
              message.error(localizeErrorCode('2000509'));
            });
        })
        .catch((err) => {
          console.error('Failed to get presign URL:', err);
        });
    }

    return (
      <Modal
        title={t("chat.batchChat")}
        width={1000}
        open={visible}
        maskClosable={false}
        closable={false}
        footer={null}
      >
        <Steps current={current} items={items} className="!p-6" />
        <div className="batch-chat-content px-10">
          {current === 0 && (
            <div className="batch-chat-content-item mt-8">
              <Form
                form={form}
                labelCol={{ span: 2 }}
                onFinish={onFinish}
                initialValues={initialValues}
              >
                <Form.Item label={t("chat.configKnowledgeBase")} name="knowledge">
                  <Select
                    options={knowledgeBaseList.map((knowledgeBase) => ({
                      value: knowledgeBase.dataset_id,
                      label: knowledgeBase.display_name,
                    }))}
                    placeholder={t("chat.selectKnowledgeBase")}
                  />
                </Form.Item>
                {}
                <Form.Item label={t("chat.file")} name="file">
                  <Dragger
                    showUploadList={false}
                    maxCount={1}
                    accept={".xlsx"}
                    beforeUpload={() => false}
                    onChange={(info) => {
                      if (!uploadFile?.uid?.length) {
                        uploadFileChange(info.file as RcFile);
                      } else {
                        message.warning(t("chat.maxUploadOneFile"));
                      }
                    }}
                    className="drag-upload-container"
                  >
                    <p className="ant-upload-drag-icon">
                      <InboxOutlined />
                    </p>
                    <p className="ant-upload-text">
                      <span style={{ marginRight: 4 }}>{t("chat.clickOrDragUpload")}</span>
                      <RiskTip />
                    </p>
                    <p className="ant-upload-hint">
                      {t("chat.uploadExcelHint")}
                      <br />
                      {t("chat.uploadExcelHint2")}
                    </p>
                  </Dragger>
                </Form.Item>
                <Form.Item>
                  {uploadFile?.uid?.length && (
                    <Flex>
                      <PaperClipOutlined />
                      <div style={{ margin: "0 20px" }}>{uploadFile?.name}</div>
                      <DeleteOutlined
                        onClick={() => {
                          if (uploadFile?.uid.length) {
                            setUploadFile(undefined);
                          }
                        }}
                      />
                    </Flex>
                  )}
                </Form.Item>
                <Form.Item>
                  <div className="mt-8 flex items-center justify-between">
                    <div>
                      <Alert
                        message={
                          <span>
                            {t("common.view")}
                            <a
                              href={templateUrl}
                              target="_self"
                              download={t("chat.batchChatTemplateFileName")}
                            >
                              {t("chat.template")}
                            </a>
                          </span>
                        }
                        type="warning"
                        showIcon
                      />
                    </div>
                    <div>
                      <Button onClick={() => setVisible(false)}>{t("common.cancel")}</Button>
                      <Button type="primary" htmlType="submit" className="ml-4">
                        {t("common.confirm")}
                      </Button>
                    </div>
                  </div>
                </Form.Item>
              </Form>
            </div>
          )}
          {current === 1 && (
            <div className="batch-chat-content-item">
              {loading ? (
                <div>
                  <Empty
                    description={
                      <div className="flex items-center justify-center gap-4">
                        <Spin indicator={<LoadingOutlined spin />} />
                        <p>
                          {t("chat.processingResult", {
                            success: batchChatTaskResult.success_num ?? 0,
                            total: batchChatTaskResult.total_num ?? 0,
                          })}
                        </p>
                      </div>
                    }
                  />
                </div>
              ) : (
                <Table
                  columns={columns}
                  dataSource={dataSource}
                  pagination={false}
                />
              )}
              <div className="mt-8 flex items-center justify-between">
                <div>
                  {loading && (
                    <Alert
                      message={t("chat.previewTopRecords", {
                        total: batchChatTaskResult.total_num,
                        count:
                          batchChatTaskResult.total_num! >= 10
                            ? 10
                            : batchChatTaskResult.total_num,
                      })}
                      type="warning"
                      showIcon
                    />
                  )}
                </div>
                <div>
                  {!loading && (
                    <Button
                      onClick={() => {
                        clearInterval(timer);
                        cancelFn("false");
                        localStorage.setItem("batchChatTask", "false");
                        localStorage.setItem("batchChatJobId", "");
                        setCurrent(0);
                        form.resetFields();
                        setUploadFile(undefined);
                        setVisible(false);
                      }}
                    >
                      {t("common.close")}
                    </Button>
                  )}
                  <Button
                    className="ml-4"
                    onClick={() => {
                      clearInterval(timer);
                      cancelFn("false");
                      localStorage.setItem("batchChatTask", "false");
                      localStorage.setItem("batchChatJobId", "");
                      setCurrent(0);
                      form.resetFields();
                      setUploadFile(undefined);
                      if (loading) {
                        setVisible(false);
                      }
                    }}
                  >
                    {loading ? t("common.cancel") : t("chat.reimport")}
                  </Button>
                  <Button
                    type="primary"
                    onClick={() => {
                      if (loading) {
                        localStorage.setItem("batchChatTask", "true");
                        cancelFn("true");
                      } else {
                        if (batchChatTaskResult?.result_file_uri) {
                          downloadUrl(batchChatTaskResult?.result_file_uri);
                        }
                        cancelFn("false");
                        localStorage.setItem("batchChatTask", "false");
                      }
                      setVisible(false);
                    }}
                    className="ml-4"
                  >
                    {loading ? t("chat.minimize") : t("chat.exportResultAction")}
                  </Button>
                </div>
              </div>
            </div>
          )}
        </div>
      </Modal>
    );
  },
);

BatchChatComponent.displayName = "BatchChatComponent";

export default BatchChatComponent;
