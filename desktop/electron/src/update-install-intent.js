const fs = require("node:fs");
const path = require("node:path");
const { randomUUID } = require("node:crypto");
const id = value => typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$/.test(value);

function valid(record) {
  return record && record.version === 1 && ["pending", "installing", "repair"].includes(record.phase)
    && [record.sourceVersion, record.nextVersion, record.releaseId].every(id)
    && (record.dataProfile === undefined || id(record.dataProfile))
    && (record.attemptId === undefined || (typeof record.attemptId === "string" && /^[a-f0-9]{32}$/.test(record.attemptId)))
    && (record.takeoverPending === undefined || (record.takeoverPending === true && record.phase === "pending" && Boolean(record.attemptId)))
    && (record.packagePath === undefined || (typeof record.packagePath === "string" && record.packagePath.length < 4096 && path.isAbsolute(record.packagePath) && !record.packagePath.includes("\0")))
    && Object.keys(record).every(key => ["version", "phase", "sourceVersion", "nextVersion", "releaseId", "dataProfile", "packagePath", "attemptId", "takeoverPending"].includes(key));
}
// Separate from the download-only reminder. This record is never authority to
// install: continuation must rediscover policy and revalidate cached bytes.
function recordFile(filename, validate) {
  const syncDirectory = () => {
    if (process.platform === "win32") return;
    const fd = fs.openSync(path.dirname(filename), "r");
    try { fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
  };
  return {
    read() {
      let fd;
      try {
        fd = fs.openSync(filename, fs.constants.O_RDONLY | (fs.constants.O_NOFOLLOW || 0));
        const stat = fs.fstatSync(fd);
        if (!stat.isFile() || stat.size > 8192) throw new Error("update_record_read");
        const buffer = Buffer.alloc(8193); const length = fs.readSync(fd, buffer, 0, buffer.length, 0);
        if (length > 8192) throw new Error("update_record_read");
        const record = JSON.parse(buffer.subarray(0, length).toString("utf8"));
        if (!validate(record)) throw new Error("update_record_read");
        return record;
      } catch (error) { if (error.code === "ENOENT") return null; throw error; }
      finally { if (fd !== undefined) fs.closeSync(fd); }
    },
    write(record) {
      if (!validate(record)) throw new Error("update_record_write");
      fs.mkdirSync(path.dirname(filename), { recursive: true, mode: 0o700 });
      const temporary = `${filename}.${randomUUID()}.tmp`; let fd;
      try {
        fd = fs.openSync(temporary, "wx", 0o600); fs.writeFileSync(fd, JSON.stringify(record)); fs.fsyncSync(fd);
        fs.closeSync(fd); fd = undefined; fs.renameSync(temporary, filename); syncDirectory();
      } finally { if (fd !== undefined) fs.closeSync(fd); try { fs.unlinkSync(temporary); } catch (e) { if (e.code !== "ENOENT") throw e; } }
    },
    clear() { try { fs.unlinkSync(filename); syncDirectory(); } catch (error) { if (error.code !== "ENOENT") throw error; } },
  };
}

function createInstallIntentStore(filename) {
  const cancellation = recordFile(filename + ".cancel.json", r => r && r.version === 1
    && ["later", "incompatible"].includes(r.kind) && [r.sourceVersion, r.nextVersion, r.releaseId].every(id)
    && typeof r.attemptId === "string" && /^[a-f0-9]{32}$/.test(r.attemptId)
    && Object.keys(r).every(k => ["version", "kind", "sourceVersion", "nextVersion", "releaseId", "attemptId"].includes(k)));
  return { ...recordFile(filename, valid), cancellation };
}
module.exports = { createInstallIntentStore };

// v2 uses a backend-owned intent. The existing v1 file remains readable.
function createCLIInstallIntentStore(filename, { invokeSync, invoke }) {
  const legacy = createInstallIntentStore(filename);
  const readBackend = () => {
    const raw = invokeSync("inspect-store");
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 1024 * 1024) throw new Error("update_record_read");
    const value = JSON.parse(raw);
    if (value.status === "blocked") throw new Error("update_record_read");
    return value.transactionId ? value : null;
  };
  return {
    cancellation: legacy.cancellation,
    read() { return readBackend() || legacy.read(); },
    write(record) {
      if (record.version !== 2 && !record.transactionId) return legacy.write(record);
      if (!record.transactionId || !["installing", "repair"].includes(record.phase)) return;
      return invoke(record.phase, record.transactionId, record.epoch).then(raw => {
        const result = typeof raw === "string" ? JSON.parse(raw) : raw;
        if (result?.status !== record.phase) throw new Error("update_record_write");
      });
    },
    clear(version) {
      if (version === 1) return legacy.clear();
      const active = readBackend();
      if (active?.transactionId) {
        const result = JSON.parse(invokeSync("cancel", active.transactionId));
        if (result.status !== "cancelled") throw new Error("update_record_write");
      } else legacy.clear();
    },
  };
}
module.exports.createCLIInstallIntentStore = createCLIInstallIntentStore;
