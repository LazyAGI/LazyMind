const fs = require("node:fs");
const path = require("node:path");
const { randomUUID } = require("node:crypto");
const id = value => typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$/.test(value);

function valid(record) {
  return record && record.version === 1 && ["pending", "installing", "repair"].includes(record.phase)
    && [record.sourceVersion, record.nextVersion, record.releaseId].every(id)
    && (record.dataProfile === undefined || id(record.dataProfile))
    && (record.packagePath === undefined || (typeof record.packagePath === "string" && record.packagePath.length < 4096 && path.isAbsolute(record.packagePath) && !record.packagePath.includes("\0")))
    && Object.keys(record).every(key => ["version", "phase", "sourceVersion", "nextVersion", "releaseId", "dataProfile", "packagePath"].includes(key));
}
// Separate from the download-only reminder. This record is never authority to
// install: continuation must rediscover policy and revalidate cached bytes.
function createInstallIntentStore(filename) {
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
        if (!valid(record)) throw new Error("update_record_read");
        return record;
      } catch (error) { if (error.code === "ENOENT") return null; throw error; }
      finally { if (fd !== undefined) fs.closeSync(fd); }
    },
    write(record) {
      if (!valid(record)) throw new Error("update_record_write");
      fs.mkdirSync(path.dirname(filename), { recursive: true, mode: 0o700 });
      const temporary = `${filename}.${randomUUID()}.tmp`; let fd;
      try {
        fd = fs.openSync(temporary, "wx", 0o600); fs.writeFileSync(fd, JSON.stringify(record)); fs.fsyncSync(fd);
        fs.closeSync(fd); fd = undefined; fs.renameSync(temporary, filename);
      } finally { if (fd !== undefined) fs.closeSync(fd); try { fs.unlinkSync(temporary); } catch (e) { if (e.code !== "ENOENT") throw e; } }
    },
    clear() { try { fs.unlinkSync(filename); } catch (error) { if (error.code !== "ENOENT") throw error; } },
  };
}
module.exports = { createInstallIntentStore };

// v2 has one backend-owned intent. The frontend holds only a transient view;
// the existing v1 file remains readable for already-published old consumers.
function createCLIInstallIntentStore(filename, { invokeSync, invoke }) {
  const legacy = createInstallIntentStore(filename);
  let view;
  const readBackend = () => {
    const raw = invokeSync("inspect-store");
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 1024 * 1024) throw new Error("update_record_read");
    const value = JSON.parse(raw);
    if (value.status === "blocked") throw new Error("update_record_read");
    return value.transactionId ? value : null;
  };
  return {
    read() { view = readBackend(); return view || legacy.read(); },
    write(record) {
      if (record.version !== 2 && !record.transactionId) return legacy.write(record);
      if (!record.transactionId) { view = record; return; }
      const tx = record.transactionId;
      if (record.phase === "installing" || record.phase === "repair") {
        return invoke(record.phase, tx, record.epoch).then(raw => {
          const result = typeof raw === "string" ? JSON.parse(raw) : raw;
          if (result?.status !== record.phase) throw new Error("update_record_write");
          view = record;
        });
      }
      view = record;
    },
    clear() {
      const active = readBackend();
      if (active?.transactionId) {
        const result = JSON.parse(invokeSync("cancel", active.transactionId));
        if (result.status !== "cancelled") throw new Error("update_record_write");
      } else legacy.clear();
      view = undefined;
    },
  };
}
module.exports.createCLIInstallIntentStore = createCLIInstallIntentStore;
