import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({
  axiosInstance: {
    defaults: {},
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
    put: vi.fn(),
  },
  BASE_URL: "http://mock-base",
}));

import { axiosInstance } from "@/components/request";
import {
  ChatFileServiceApi,
  ChatServiceApi,
  ConversationSettingsApi,
  DatabaseBaseServiceApi,
  DocumentServiceApi,
  KnowledgeBaseServiceApi,
  PluginInfoApi,
  PluginSessionApi,
  PromptServiceApi,
  TaskServiceApi,
  TempUploadServiceApi,
  convEventsUrl,
  decideToolLimit,
  estimateContextUsage,
  exportContextPrompt,
  parseConversationPluginSettings,
  taskStreamUrl,
} from "./request";

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;
const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;
const mockedPatch = axiosInstance.patch as unknown as ReturnType<typeof vi.fn>;
const mockedDelete = axiosInstance.delete as unknown as ReturnType<typeof vi.fn>;
const mockedPut = axiosInstance.put as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedGet.mockReset().mockResolvedValue({ data: {} });
  mockedPost.mockReset().mockResolvedValue({ data: {} });
  mockedPatch.mockReset().mockResolvedValue({ data: {} });
  mockedDelete.mockReset().mockResolvedValue({ data: {} });
  mockedPut.mockReset().mockResolvedValue({ data: {} });
});

describe("estimateContextUsage", () => {
  it("posts the payload and unwraps response.data.data", async () => {
    mockedPost.mockResolvedValueOnce({ data: { data: { estimated_tokens: 42 } } });

    const result = await estimateContextUsage({ query: "hi" });

    expect(mockedPost).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations:estimateContextUsage",
      { query: "hi" },
    );
    expect(result).toEqual({ estimated_tokens: 42 });
  });
});

describe("exportContextPrompt", () => {
  it("posts with blob responseType and returns the blob", async () => {
    const blob = new Blob(["content"]);
    mockedPost.mockResolvedValueOnce({ data: blob });

    const result = await exportContextPrompt({ query: "hi" });

    expect(mockedPost).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations:exportContextPrompt",
      { query: "hi" },
      { responseType: "blob" },
    );
    expect(result).toBe(blob);
  });
});

describe("URL builder helpers", () => {
  it("taskStreamUrl encodes the task id", () => {
    expect(taskStreamUrl("task 1")).toBe(
      "http://mock-base/api/core/tasks/task%201:stream",
    );
  });

  it("convEventsUrl encodes the conversation id", () => {
    expect(convEventsUrl("conv/1")).toBe(
      "http://mock-base/api/core/conversations/conv%2F1/events",
    );
  });
});

describe("decideToolLimit", () => {
  it("posts the decision action for the given conversation", async () => {
    await decideToolLimit("conv-1", "decision-1", "summarize");

    expect(mockedPost).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations/conv-1:toolLimitDecision",
      { decision_id: "decision-1", action: "summarize" },
    );
  });
});

describe("TaskServiceApi", () => {
  it("builds correct URLs for each task/artifact endpoint", async () => {
    const api = TaskServiceApi();

    await api.listConversationTasks("conv-1");
    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations/conv-1/tasks",
      undefined,
    );

    await api.getTaskDetail("task-1");
    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/tasks/task-1",
      undefined,
    );

    await api.getTaskArtifacts("task-1");
    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/tasks/task-1/artifacts",
      undefined,
    );
  });
});

describe("PluginInfoApi", () => {
  it("fetches a single plugin and the plugin list", async () => {
    const api = PluginInfoApi();

    await api.getPlugin("plugin-1");
    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/plugins/plugin-1",
      undefined,
    );

    await api.listPlugins();
    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/plugins",
      undefined,
    );
  });
});

describe("PluginSessionApi", () => {
  it("patches a slot with the selected revision", async () => {
    const api = PluginSessionApi();

    await api.patchSlot("session-1", "slot-1", 3);

    expect(mockedPatch).toHaveBeenCalledWith(
      "http://mock-base/api/core/plugin-sessions/session-1/slots/slot-1",
      { selected_revision: 3 },
      undefined,
    );
  });

  it("deletes a slot item and includes order_version only when provided", async () => {
    const api = PluginSessionApi();

    await api.deleteSlotItem("session-1", "slot-1", 0);
    expect(mockedDelete).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/plugin-sessions/session-1/slots/slot-1/items/idx/0",
      { data: undefined },
    );

    await api.deleteSlotItem("session-1", "slot-1", 0, 5);
    expect(mockedDelete).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/plugin-sessions/session-1/slots/slot-1/items/idx/0",
      { data: { order_version: 5 } },
    );
  });

  it("patches a slot item and only includes optional fields when set", async () => {
    const api = PluginSessionApi();

    await api.patchSlotItem("session-1", "slot-1", 2, "value");
    expect(mockedPatch).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/plugin-sessions/session-1/slots/slot-1/items/idx/2",
      { value: "value" },
      undefined,
    );

    await api.patchSlotItem("session-1", "slot-1", 2, "value", "text/plain", "draft");
    expect(mockedPatch).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/plugin-sessions/session-1/slots/slot-1/items/idx/2",
      { value: "value", content_type: "text/plain", mode: "draft" },
      undefined,
    );
  });

  it("dismisses and restores a session with JSON headers merged into options", async () => {
    const api = PluginSessionApi();

    await api.dismissSession("session-1");
    expect(mockedPost).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/plugin-sessions/session-1:dismiss",
      {},
      { headers: { "Content-Type": "application/json" } },
    );

    await api.restoreSession("session-1");
    expect(mockedPost).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/plugin-sessions/session-1:restore",
      {},
      { headers: { "Content-Type": "application/json" } },
    );
  });
});

describe("ChatServiceApi", () => {
  it("builds conversation list query params from request parameters", async () => {
    const api = ChatServiceApi();

    await api.conversationServiceListConversations({
      pageToken: "tok",
      pageSize: 10,
      keyword: "foo",
    });

    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations",
      {
        params: { page_token: "tok", page_size: 10, keyword: "foo" },
      },
    );
  });

  it("saves ask answers with the JSON content-type header", async () => {
    const api = ChatServiceApi();

    await api.conversationServiceSaveAskAnswers("conv-1", "hist-1", { q1: "a1" });

    expect(mockedPatch).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations/conv-1:ask-answers",
      { history_id: "hist-1", answers: { q1: "a1" } },
      { headers: { "Content-Type": "application/json" } },
    );
  });

  it("deletes a conversation by encoded id", async () => {
    const api = ChatServiceApi();

    await api.conversationServiceDeleteConversation({ conversation: "conv 1" });

    expect(mockedDelete).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations/conv%201",
      undefined,
    );
  });
});

describe("PromptServiceApi", () => {
  it("lists prompts with all filter params forwarded", async () => {
    const api = PromptServiceApi();

    await api.listPrompts({ pageSize: 5, keyword: "kw", category: "cat" });

    expect(mockedGet).toHaveBeenCalledWith("http://mock-base/api/core/prompts", {
      params: {
        page_size: 5,
        page_token: undefined,
        keyword: "kw",
        category: "cat",
        scope: undefined,
        sort: undefined,
        locale: undefined,
      },
    });
  });

  it("marks usePrompt requests as silentError", async () => {
    const api = PromptServiceApi();

    await api.usePrompt("prompt-1");

    expect(mockedPost).toHaveBeenCalledWith(
      "http://mock-base/api/core/prompts/prompt-1:use",
      undefined,
      { silentError: true },
    );
  });
});

describe("DocumentServiceApi / KnowledgeBaseServiceApi / DatabaseBaseServiceApi", () => {
  it("fetch document creators and tags", async () => {
    const api = DocumentServiceApi();
    await api.documentServiceAllDocumentCreators();
    await api.documentServiceAllDocumentTags();

    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/document/creators",
      undefined,
    );
    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/document/tags",
      undefined,
    );
  });

  it("sets and unsets the default dataset", async () => {
    const api = KnowledgeBaseServiceApi();

    await api.datasetServiceSetDefaultDataset("ds-1", "name-1");
    expect(mockedPost).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/datasets/ds-1:setDefault",
      { name: "name-1" },
      { headers: { "Content-Type": "application/json" } },
    );

    await api.datasetServiceUnsetDefaultDataset("ds-1", "name-1");
    expect(mockedPost).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/datasets/ds-1:unsetDefault",
      { name: "name-1" },
      { headers: { "Content-Type": "application/json" } },
    );
  });

  it("fetches user database summaries", async () => {
    const api = DatabaseBaseServiceApi();
    await api.databaseServiceGetUserDatabaseSummaries();

    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/rag/databases/summary",
      undefined,
    );
  });
});

describe("ChatFileServiceApi", () => {
  it("presigns an attachment against the v1 endpoint", async () => {
    const api = ChatFileServiceApi();

    await api.fileServicePresignAttachment({
      presignAttachmentRequest: { filename: "a.png" } as never,
    });

    expect(mockedPost).toHaveBeenCalledWith(
      "http://mock-base/api/v1/attachment:presign",
      { filename: "a.png" },
      { headers: { "Content-Type": "application/json" } },
    );
  });
});

describe("TempUploadServiceApi", () => {
  it("wires init/upload/complete/abort to the expected temp upload endpoints", async () => {
    const api = TempUploadServiceApi();

    await api.initUpload({ filename: "a.bin" });
    expect(mockedPost).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/temp/uploads:initUpload",
      { filename: "a.bin" },
      { headers: { "Content-Type": "application/json" } },
    );

    const blob = new Blob(["x"]);
    await api.uploadPart("up-1", 1, blob);
    expect(mockedPut).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/temp/uploads/up-1/parts/1",
      blob,
      { headers: { "Content-Type": "application/octet-stream" } },
    );

    await api.completeUpload("up-1", { auto_start: false });
    expect(mockedPost).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/temp/uploads/up-1:complete",
      { auto_start: false },
      { headers: { "Content-Type": "application/json" } },
    );

    await api.abortUpload("up-1");
    expect(mockedPost).toHaveBeenLastCalledWith(
      "http://mock-base/api/core/temp/uploads/up-1:abort",
      {},
      { headers: { "Content-Type": "application/json" } },
    );
  });
});

describe("parseConversationPluginSettings", () => {
  it("returns undefined when the conversation is missing", () => {
    expect(parseConversationPluginSettings(null)).toBeUndefined();
    expect(parseConversationPluginSettings(undefined)).toBeUndefined();
  });

  it("returns undefined when no relevant fields are set", () => {
    expect(parseConversationPluginSettings({})).toBeUndefined();
  });

  it("only includes plugin_mode when it is a recognized value", () => {
    expect(
      parseConversationPluginSettings({ plugin_mode: "invalid" }),
    ).toBeUndefined();
    expect(
      parseConversationPluginSettings({ plugin_mode: "dynamic" }),
    ).toEqual({ plugin_mode: "dynamic" });
  });

  it("collects all provided boolean/mode fields", () => {
    expect(
      parseConversationPluginSettings({
        enable_plugin: true,
        plugin_mode: "auto",
        enable_subagent: false,
      }),
    ).toEqual({
      enable_plugin: true,
      plugin_mode: "auto",
      enable_subagent: false,
    });
  });
});

describe("ConversationSettingsApi", () => {
  it("gets and patches chat settings", async () => {
    const api = ConversationSettingsApi();

    await api.getChatSettings();
    expect(mockedGet).toHaveBeenCalledWith(
      "http://mock-base/api/core/user/chat-settings",
      undefined,
    );

    await api.patchPluginSettings("conv-1", { enable_plugin: true });
    expect(mockedPatch).toHaveBeenCalledWith(
      "http://mock-base/api/core/conversations/conv-1/plugin-settings",
      { enable_plugin: true },
      undefined,
    );
  });
});
