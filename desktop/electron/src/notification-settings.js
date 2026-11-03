const os = require("node:os");

function registerNotificationSettingsIPC({ ipcMain, shell, isTrustedSender, platform = process.platform, systemRelease = os.release() }) {
  ipcMain.handle("lazymind:openNotificationSettings", async (event) => {
    if (!isTrustedSender(event)) {
      throw new Error("Notification settings are only available from the LazyMind main window");
    }

    // Ventura and later use a System Settings extension; Monterey uses a pane.
    const url = platform === "darwin"
      ? Number.parseInt(systemRelease, 10) >= 22
        ? "x-apple.systempreferences:com.apple.Notifications-Settings.extension"
        : "x-apple.systempreferences:com.apple.preference.notifications"
      : platform === "win32" ? "ms-settings:notifications" : null;
    if (!url) throw new Error("Opening notification settings is not supported on this platform");

    // The renderer supplies no URL: this command opens only the OS settings page.
    await shell.openExternal(url);
  });
}

module.exports = { registerNotificationSettingsIPC };
