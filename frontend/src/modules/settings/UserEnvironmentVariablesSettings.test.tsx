import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConfigProvider, message } from "antd";
import { StrictMode } from "react";
import { createInstance } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import zhCN from "@/i18n/locales/zh-CN";
import enUS from "@/i18n/locales/en-US";
import UserEnvironmentVariablesSettings, { validateEnvName } from "./UserEnvironmentVariablesSettings";
import envNameCases from "../../../../tests/contracts/env_names.json";
import * as api from "./userEnvApi";

vi.mock("./userEnvApi", () => ({
  listUserEnvironmentVariables: vi.fn(),
  createUserEnvironmentVariable: vi.fn(),
  patchUserEnvironmentVariable: vi.fn(),
  deleteUserEnvironmentVariable: vi.fn(),
}));

const row: api.UserEnvironmentVariable = {
  id: "env-test", name: "service_token", masked_value: "abcd****wxyz",
  enabled: true, description: "Search", created_at: "2026-09-20T00:00:00Z",
  updated_at: "2026-09-20T00:00:00Z",
};

const i18n = createInstance();

beforeEach(async () => {
  vi.resetAllMocks();
  await i18n.use(initReactI18next).init({
    lng: "zh-CN",
    fallbackLng: false,
    resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } },
    interpolation: { escapeValue: false },
  });
  vi.mocked(api.listUserEnvironmentVariables).mockResolvedValue([{ ...row }]);
});

function renderSettings(strictMode = false) {
  const settings = (
    <I18nextProvider i18n={i18n}>
      <ConfigProvider theme={{ token: { motion: false } }}>
        <UserEnvironmentVariablesSettings />
      </ConfigProvider>
    </I18nextProvider>
  );
  return render(strictMode ? <StrictMode>{settings}</StrictMode> : settings);
}

describe("User environment variables", () => {
  it.each(envNameCases)("shares the environment name contract for $name", async ({ name, valid }) => {
    const result = validateEnvName(i18n.t, name);
    if (valid) await expect(result).resolves.toBeUndefined();
    else await expect(result).rejects.toThrow();
  });
  it("keeps the Chinese and English translation keys aligned", () => {
    expect(Object.keys(zhCN.settingsPage.envVars).sort()).toEqual(Object.keys(enUS.settingsPage.envVars).sort());
  });

  it.each([
    ["zh-CN", zhCN],
    ["en-US", enUS],
  ] as const)("localizes the list, dialogs and validation in %s", async (language, messages) => {
    await i18n.changeLanguage(language);
    renderSettings();
    await screen.findByRole("button", { name: "abcd****wxyz" });
    const copy = messages.settingsPage.envVars;
    expect(screen.getByRole("heading", { name: messages.settingsPage.sections.envVars })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: new RegExp(copy.add) }));
    let dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveAccessibleName(copy.createTitle);
    expect(within(dialog).getByText(copy.nameHelp)).toBeInTheDocument();
    expect(copy.nameHelp).not.toMatch(/TAVILY_API_KEY|openai_token/);
    fireEvent.change(within(dialog).getByLabelText(copy.name), { target: { value: "11" } });
    fireEvent.click(within(dialog).getByRole("button", { name: new RegExp(messages.common.save.split("").join("\\s*")) }));
    expect(await within(dialog).findByText(copy.nameInvalid)).toBeInTheDocument();
    expect(api.createUserEnvironmentVariable).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: new RegExp(messages.common.cancel.split("").join("\\s*")) }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "abcd****wxyz" }));
    dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveAccessibleName(copy.editTitle);
    expect(within(dialog).getByText(copy.nameHelp)).toBeInTheDocument();
    expect(within(dialog).getByText(copy.secretUnchanged)).toBeInTheDocument();
    if (language === "en-US") expect(dialog.textContent).not.toMatch(/[\u4e00-\u9fff]/);
  });

  it("updates the open editor when the language changes", async () => {
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    await screen.findByRole("dialog");
    await act(async () => { await i18n.changeLanguage("en-US"); });
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAccessibleName(enUS.settingsPage.envVars.editTitle);
    expect(within(dialog).getByText(enUS.settingsPage.envVars.nameHelp)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(enUS.settingsPage.envVars.name)).toHaveValue(row.name);
    expect(dialog.textContent).not.toMatch(/[\u4e00-\u9fff]/);
  });

  it.each(["create", "edit"])("preserves credential whitespace on %s", async (mode) => {
    vi.mocked(api.createUserEnvironmentVariable).mockResolvedValue({ ...row, id: "new-env", name: "new_token" });
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValue(row);
    renderSettings();
    const edit = await screen.findByRole("button", { name: "abcd****wxyz" });
    fireEvent.click(mode === "edit" ? edit : screen.getByRole("button", { name: /新增变量/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("变量名"), { target: { value: mode === "edit" ? row.name : "new_token" } });
    fireEvent.change(within(dialog).getByLabelText(mode === "edit" ? "新密钥" : "密钥"), { target: { value: " synthetic-password " } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    if (mode === "edit") {
      expect(api.patchUserEnvironmentVariable).toHaveBeenCalledWith(row.id, expect.objectContaining({ value: " synthetic-password " }));
    } else {
      expect(api.createUserEnvironmentVariable).toHaveBeenCalledWith(expect.objectContaining({ value: " synthetic-password " }));
    }
  });

  it("rejects a whitespace-only replacement instead of treating it as unchanged", async () => {
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("新密钥"), { target: { value: "   " } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    expect(await within(dialog).findByText("密钥不能仅包含空白字符")).toBeInTheDocument();
    expect(api.patchUserEnvironmentVariable).not.toHaveBeenCalled();
  });

  it.each(["zh-CN", "en-US"] as const)("identifies reserved names without suggesting key recovery in %s", async (language) => {
    await i18n.changeLanguage(language);
    const copy = language === "zh-CN" ? zhCN.settingsPage.envVars : enUS.settingsPage.envVars;
    const invalid = { ...row, name: "NODE_TLS_REJECT_UNAUTHORIZED", credential_status: "invalid_name" as const };
    vi.mocked(api.listUserEnvironmentVariables).mockResolvedValue([invalid]);
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValue({ ...invalid, enabled: false });
    renderSettings();
    const status = await screen.findByRole("button", { name: copy.invalidNameTitle });
    const toggle = screen.getByRole("switch");
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
    fireEvent.click(status);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(copy.invalidNameDescription)).toBeInTheDocument();
    expect(within(dialog).queryByText(copy.unavailableDescription)).not.toBeInTheDocument();
  });

  it("keeps an unavailable credential visible, disableable and replaceable", async () => {
    const unavailable = { ...row, credential_status: "unavailable" as const, masked_value: "••••••••" };
    vi.mocked(api.listUserEnvironmentVariables).mockResolvedValue([unavailable]);
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValueOnce({ ...unavailable, enabled: false });
    renderSettings();
    const toggle = await screen.findByRole("switch", { name: "启用 service_token" });
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
    expect(api.patchUserEnvironmentVariable).toHaveBeenCalledWith(row.id, { enabled: false, expected_updated_at: row.updated_at });
    fireEvent.click(screen.getByRole("button", { name: "密钥不可用" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("当前密钥不可用")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("新密钥")).toHaveValue("");
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValueOnce({ ...row, enabled: false, credential_status: "available" });
    fireEvent.change(within(dialog).getByLabelText("新密钥"), { target: { value: " synthetic-replacement " } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.patchUserEnvironmentVariable).toHaveBeenLastCalledWith(row.id, { value: " synthetic-replacement ", expected_updated_at: row.updated_at });
    expect(screen.queryByRole("button", { name: "密钥不可用" })).not.toBeInTheDocument();
  });

  it.each(["before_latest", "after_create"])("ignores a stale load resolving %s", async (order) => {
    let finishOld!: (items: api.UserEnvironmentVariable[]) => void;
    let finishLatest!: (items: api.UserEnvironmentVariable[]) => void;
    vi.mocked(api.listUserEnvironmentVariables)
      .mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve; }))
      .mockImplementationOnce(() => new Promise((resolve) => { finishLatest = resolve; }));
    vi.mocked(api.createUserEnvironmentVariable).mockResolvedValue(row);
    renderSettings(true);
    expect(api.listUserEnvironmentVariables).toHaveBeenCalledTimes(2);
    const create = screen.getByRole("button", { name: /新增变量/ });
    expect(create).toBeDisabled();
    fireEvent.click(create);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    const stale = { ...row, id: "stale-id", name: "stale_token" };
    if (order === "before_latest") {
      await act(async () => finishOld([stale]));
      expect(create).toBeDisabled();
      expect(screen.queryByText("stale_token")).not.toBeInTheDocument();
    }
    await act(async () => finishLatest([]));
    expect(create).not.toBeDisabled();
    fireEvent.click(create);
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("变量名"), { target: { value: row.name } });
    fireEvent.change(within(dialog).getByLabelText("密钥"), { target: { value: "synthetic-test-value" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByText(row.name)).toBeInTheDocument();
    if (order === "after_create") {
      await act(async () => finishOld([stale]));
    }
    expect(screen.getByText(row.name)).toBeInTheDocument();
    expect(screen.queryByText("stale_token")).not.toBeInTheDocument();
    expect(api.createUserEnvironmentVariable).toHaveBeenCalledTimes(1);
  });

  it("unlocks creation after the initial load fails", async () => {
    let fail!: (reason: Error) => void;
    vi.mocked(api.listUserEnvironmentVariables).mockImplementation(() => new Promise((_, reject) => { fail = reject; }));
    renderSettings();
    const create = screen.getByRole("button", { name: /新增变量/ });
    expect(create).toBeDisabled();
    await act(async () => fail(new Error("request failed")));
    expect(create).not.toBeDisabled();
    fireEvent.click(create);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("blocks closing and duplicate saves until the pending edit completes", async () => {
    let finish!: (value: api.UserEnvironmentVariable) => void;
    vi.mocked(api.patchUserEnvironmentVariable).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "Updated note" } });
    const save = within(dialog).getByRole("button", { name: /保\s*存/ });
    fireEvent.click(save);
    fireEvent.click(save);
    await waitFor(() => expect(api.patchUserEnvironmentVariable).toHaveBeenCalledTimes(1));
    const cancel = within(dialog).getByRole("button", { name: /Cancel|取\s*消/ });
    expect(cancel).toBeDisabled();
    expect(within(dialog).queryByRole("button", { name: /close/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /新增变量/ })).toBeDisabled();
    expect(within(dialog).getByLabelText("变量名")).toBeDisabled();
    fireEvent.click(cancel);
    fireEvent.keyDown(dialog, { key: "Escape", keyCode: 27 });
    expect(dialog).toBeInTheDocument();
    await act(async () => finish(row));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /新增变量/ }));
    const nextDialog = await screen.findByRole("dialog");
    expect(within(nextDialog).getByLabelText("变量名")).toHaveValue("");
    expect(within(nextDialog).getByLabelText("变量名")).not.toBeDisabled();
  });

  it("unlocks the edit dialog after a failed save", async () => {
    vi.mocked(api.patchUserEnvironmentVariable).mockRejectedValue(new Error("request failed"));
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "Updated note" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(api.patchUserEnvironmentVariable).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(dialog).getByRole("button", { name: /Cancel|取\s*消/ })).not.toBeDisabled());
    expect(within(dialog).getByLabelText("变量名")).not.toBeDisabled();
    fireEvent.click(within(dialog).getByRole("button", { name: /Cancel|取\s*消/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("serializes toggle requests and preserves the confirmed state on failure", async () => {
    let reject!: (reason: Error) => void;
    vi.mocked(api.patchUserEnvironmentVariable).mockImplementation(() => new Promise((_, fail) => { reject = fail; }));
    const toast = vi.spyOn(message, "error");
    renderSettings();
    const toggle = await screen.findByRole("switch", { name: "启用 service_token" });
    fireEvent.click(toggle);
    fireEvent.click(toggle);
    expect(api.patchUserEnvironmentVariable).toHaveBeenCalledTimes(1);
    expect(toggle).toBeDisabled();
    await act(async () => reject(new Error("already displayed by interceptor")));
    await waitFor(() => expect(toggle).not.toBeDisabled());
    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(toast).not.toHaveBeenCalled();
    toast.mockRestore();
  });

  it("rejects invalid names locally and preserves lowercase names", async () => {
    vi.mocked(api.listUserEnvironmentVariables).mockResolvedValue([]);
    vi.mocked(api.createUserEnvironmentVariable).mockResolvedValue(row);
    renderSettings();
    await waitFor(() => expect(screen.getByRole("button", { name: /新增变量/ })).not.toBeDisabled());
    fireEvent.click(screen.getByRole("button", { name: /新增变量/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("变量名"), { target: { value: "11" } });
    fireEvent.change(within(dialog).getByLabelText("密钥"), { target: { value: "test-private-value" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    expect(await within(dialog).findByText(/环境变量名不合理/)).toBeInTheDocument();
    expect(api.createUserEnvironmentVariable).not.toHaveBeenCalled();
    fireEvent.change(within(dialog).getByLabelText("变量名"), { target: { value: "service_token" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(api.createUserEnvironmentVariable).toHaveBeenCalledWith(expect.objectContaining({ name: "service_token" })));
  });

  it("does not send the mask as a replacement value when editing metadata", async () => {
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValue(row);
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("新密钥")).toHaveValue("");
    fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "Updated note" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(api.patchUserEnvironmentVariable).toHaveBeenCalledWith(row.id, {
      description: "Updated note", expected_updated_at: row.updated_at,
    }));
  });

  it.each(["unchanged", "normalized", "reverted"])("skips a write for a %s edit", async (mode) => {
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValue(row);
    const toast = vi.spyOn(message, "success");
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    const dialog = await screen.findByRole("dialog");
    if (mode === "normalized") {
      fireEvent.change(within(dialog).getByLabelText("变量名"), { target: { value: ` ${row.name} ` } });
      fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: ` ${row.description} ` } });
    } else if (mode === "reverted") {
      fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "Updated note" } });
      fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: row.description } });
      fireEvent.click(within(dialog).getByRole("switch", { name: "启用" }));
      fireEvent.click(within(dialog).getByRole("switch", { name: "启用" }));
      fireEvent.change(within(dialog).getByLabelText("新密钥"), { target: { value: "discarded-secret" } });
      fireEvent.change(within(dialog).getByLabelText("新密钥"), { target: { value: "" } });
    }
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.patchUserEnvironmentVariable).not.toHaveBeenCalled();
    expect(api.createUserEnvironmentVariable).not.toHaveBeenCalled();
    expect(toast).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /新增变量/ })).not.toBeDisabled();
    expect(screen.getByRole("switch", { name: "启用 service_token" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText(row.description)).toBeInTheDocument();
    toast.mockRestore();
  });

  it("submits only edited metadata with the version from the open dialog", async () => {
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValue({ ...row, description: "new note" });
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "new note" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(api.patchUserEnvironmentVariable).toHaveBeenCalledWith(row.id, {
      description: "new note", expected_updated_at: row.updated_at,
    }));
  });

  it("includes explicit disable and empty description changes", async () => {
    vi.mocked(api.patchUserEnvironmentVariable).mockResolvedValue({ ...row, enabled: false, description: "" });
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "" } });
    fireEvent.click(within(dialog).getByRole("switch", { name: "启用" }));
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(api.patchUserEnvironmentVariable).toHaveBeenCalledWith(row.id, {
      enabled: false, description: "", expected_updated_at: row.updated_at,
    }));
  });

  it("preserves edits on conflict and requires reopening the refreshed record", async () => {
    const updated = { ...row, enabled: false, updated_at: "2026-09-22T00:00:00Z" };
    vi.mocked(api.listUserEnvironmentVariables).mockResolvedValueOnce([row]).mockResolvedValue([updated]);
    vi.mocked(api.patchUserEnvironmentVariable).mockRejectedValueOnce(Object.assign(new Error("conflict"), {
      isAxiosError: true, response: { status: 409 },
    })).mockResolvedValue(updated);
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "abcd****wxyz" }));
    let dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("新密钥"), { target: { value: "synthetic-new-value" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    expect(await within(dialog).findByText("变量已更新或名称冲突，请取消后重新编辑。")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("新密钥")).toHaveValue("synthetic-new-value");
    const save = within(dialog).getByRole("button", { name: /保\s*存/ });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(api.patchUserEnvironmentVariable).toHaveBeenCalledTimes(1);
    const cancel = within(dialog).getByRole("button", { name: /Cancel|取\s*消/ });
    await waitFor(() => expect(cancel).not.toBeDisabled());
    fireEvent.click(cancel);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "abcd****wxyz" }));
    dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("switch", { name: "启用" })).toHaveAttribute("aria-checked", "false");
    fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "new note" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(api.patchUserEnvironmentVariable).toHaveBeenLastCalledWith(row.id, {
      description: "new note", expected_updated_at: updated.updated_at,
    }));
  });

  it("refreshes a stale toggle instead of retrying with an old version", async () => {
    const updated = { ...row, enabled: false, updated_at: "2026-09-22T00:00:00Z" };
    vi.mocked(api.listUserEnvironmentVariables).mockResolvedValueOnce([row]).mockResolvedValue([updated]);
    vi.mocked(api.patchUserEnvironmentVariable).mockRejectedValueOnce(Object.assign(new Error("conflict"), {
      isAxiosError: true, response: { status: 409 },
    })).mockResolvedValue({ ...updated, enabled: true });
    renderSettings();
    const toggle = await screen.findByRole("switch", { name: "启用 service_token" });
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
    await waitFor(() => expect(toggle).not.toBeDisabled());
    expect(api.patchUserEnvironmentVariable).toHaveBeenCalledTimes(1);
    fireEvent.click(toggle);
    await waitFor(() => expect(api.patchUserEnvironmentVariable).toHaveBeenLastCalledWith(row.id, {
      enabled: true, expected_updated_at: updated.updated_at,
    }));
  });

  it("requires confirmation before deletion", async () => {
    vi.mocked(api.deleteUserEnvironmentVariable).mockResolvedValue(undefined);
    renderSettings();
    const deleteButton = await screen.findByRole("button", { name: "删除 service_token" });
    expect(deleteButton).toHaveAttribute("title", "删除环境变量");
    fireEvent.click(deleteButton);
    expect((await screen.findByRole("dialog")).closest(".ant-modal-wrap")).toHaveClass("ant-modal-centered");
    fireEvent.click(await screen.findByRole("button", { name: /Cancel|取\s*消/ }));
    expect(api.deleteUserEnvironmentVariable).not.toHaveBeenCalled();
    expect(deleteButton).toBeInTheDocument();
    fireEvent.click(deleteButton);
    fireEvent.click(await screen.findByRole("button", { name: /^删\s*除$/ }));
    await waitFor(() => expect(api.deleteUserEnvironmentVariable).toHaveBeenCalledWith(row.id));
    await waitFor(() => expect(screen.queryByRole("button", { name: "删除 service_token" })).not.toBeInTheDocument());
  });

  it("keeps the centered deletion dialog open on failure and blocks duplicate requests", async () => {
    let rejectDelete!: (error: Error) => void;
    vi.mocked(api.deleteUserEnvironmentVariable).mockImplementationOnce(() => new Promise((_, reject) => {
      rejectDelete = reject;
    })).mockResolvedValue(undefined);
    renderSettings();
    fireEvent.click(await screen.findByRole("button", { name: "删除 service_token" }));
    const dialog = await screen.findByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: /^删\s*除$/ });
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    expect(api.deleteUserEnvironmentVariable).toHaveBeenCalledTimes(1);
    expect(within(dialog).getByRole("button", { name: /取\s*消/ })).toBeDisabled();
    await act(async () => rejectDelete(new Error("network error")));
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: /取\s*消/ })).not.toBeDisabled();
    fireEvent.click(confirm);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.deleteUserEnvironmentVariable).toHaveBeenCalledTimes(2);
  });

  it.each(["toggle", "delete", "edit", "create"])("keeps a successful %s when another row's conflict refresh finishes late", async (action) => {
    const other = { ...row, id: "other", name: "OTHER_TOKEN" };
    const created = { ...row, id: "new", name: "NEW_TOKEN" };
    let finishReload!: (rows: api.UserEnvironmentVariable[]) => void;
    vi.mocked(api.listUserEnvironmentVariables)
      .mockResolvedValueOnce([row, other])
      .mockImplementationOnce(() => new Promise((resolve) => { finishReload = resolve; }));
    vi.mocked(api.patchUserEnvironmentVariable).mockImplementation(async (id, payload) => {
      if (id === row.id) throw Object.assign(new Error("conflict"), { isAxiosError: true, response: { status: 409 } });
      return { ...other, ...payload, updated_at: "2026-09-22T00:00:00Z" };
    });
    vi.mocked(api.deleteUserEnvironmentVariable).mockResolvedValue(undefined);
    vi.mocked(api.createUserEnvironmentVariable).mockResolvedValue(created);
    renderSettings();
    fireEvent.click(await screen.findByRole("switch", { name: "启用 service_token" }));
    await waitFor(() => expect(api.listUserEnvironmentVariables).toHaveBeenCalledTimes(2));
    if (action === "toggle") {
      fireEvent.click(screen.getByRole("switch", { name: "启用 OTHER_TOKEN" }));
      await waitFor(() => expect(screen.getByRole("switch", { name: "启用 OTHER_TOKEN" })).toHaveAttribute("aria-checked", "false"));
    } else if (action === "delete") {
      fireEvent.click(screen.getByRole("button", { name: "删除 OTHER_TOKEN" }));
      fireEvent.click(await screen.findByRole("button", { name: /^删\s*除$/ }));
      await waitFor(() => expect(screen.queryByText("OTHER_TOKEN")).not.toBeInTheDocument());
    } else {
      fireEvent.click(screen.getByRole("button", { name: action === "edit" ? "编辑 OTHER_TOKEN" : /新增变量/ }));
      const dialog = await screen.findByRole("dialog");
      if (action === "edit") {
        fireEvent.change(within(dialog).getByLabelText("备注"), { target: { value: "Updated note" } });
      } else {
        fireEvent.change(within(dialog).getByLabelText("变量名"), { target: { value: created.name } });
        fireEvent.change(within(dialog).getByLabelText("密钥"), { target: { value: "synthetic-test-value" } });
      }
      fireEvent.click(within(dialog).getByRole("button", { name: /保\s*存/ }));
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    }
    await act(async () => finishReload([{ ...row, enabled: false }, other]));
    expect(screen.getByRole("switch", { name: "启用 service_token" })).toHaveAttribute("aria-checked", "false");
    if (action === "toggle") {
      expect(screen.getByRole("switch", { name: "启用 OTHER_TOKEN" })).toHaveAttribute("aria-checked", "false");
    } else if (action === "delete") {
      expect(screen.queryByText("OTHER_TOKEN")).not.toBeInTheDocument();
    } else if (action === "edit") {
      expect(screen.getByText("Updated note")).toBeInTheDocument();
    } else {
      expect(screen.getByText("NEW_TOKEN")).toBeInTheDocument();
    }
  });

  it.each(["first", "second"])("merges independent conflict refreshes when %s resolves first", async (order) => {
    const other = { ...row, id: "other", name: "OTHER_TOKEN" };
    let finishFirst!: (rows: api.UserEnvironmentVariable[]) => void;
    let finishSecond!: (rows: api.UserEnvironmentVariable[]) => void;
    vi.mocked(api.listUserEnvironmentVariables)
      .mockResolvedValueOnce([row, other])
      .mockImplementationOnce(() => new Promise((resolve) => { finishFirst = resolve; }))
      .mockImplementationOnce(() => new Promise((resolve) => { finishSecond = resolve; }));
    vi.mocked(api.patchUserEnvironmentVariable).mockRejectedValue(Object.assign(new Error("conflict"), {
      isAxiosError: true, response: { status: 409 },
    }));
    renderSettings();
    fireEvent.click(await screen.findByRole("switch", { name: "启用 service_token" }));
    await waitFor(() => expect(api.listUserEnvironmentVariables).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole("switch", { name: "启用 OTHER_TOKEN" }));
    await waitFor(() => expect(api.listUserEnvironmentVariables).toHaveBeenCalledTimes(3));
    const finishA = () => finishFirst([{ ...row, enabled: false }, other]);
    const finishB = () => finishSecond([row, { ...other, enabled: false }]);
    await act(async () => { (order === "first" ? finishA : finishB)(); });
    await act(async () => { (order === "first" ? finishB : finishA)(); });
    expect(screen.getByRole("switch", { name: "启用 service_token" })).toHaveAttribute("aria-checked", "false");
    expect(screen.getByRole("switch", { name: "启用 OTHER_TOKEN" })).toHaveAttribute("aria-checked", "false");
  });

  it("removes a remotely deleted conflicting row without changing another row", async () => {
    const other = { ...row, id: "other", name: "OTHER_TOKEN" };
    vi.mocked(api.listUserEnvironmentVariables).mockResolvedValueOnce([row, other]).mockResolvedValueOnce([]);
    vi.mocked(api.patchUserEnvironmentVariable).mockRejectedValue(Object.assign(new Error("conflict"), {
      isAxiosError: true, response: { status: 409 },
    }));
    renderSettings();
    fireEvent.click(await screen.findByRole("switch", { name: "启用 service_token" }));
    await waitFor(() => expect(screen.queryByText("service_token")).not.toBeInTheDocument());
    expect(screen.getByText("OTHER_TOKEN")).toBeInTheDocument();
  });
});
