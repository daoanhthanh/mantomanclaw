import { fromBuffer, type Entry, type ZipFile } from "yauzl";

const MAX_ENTRIES = 256;
const MAX_EXPANDED_BYTES = 8 * 1024 * 1024;
const MAX_RATIO = 100;

export class ArchiveError extends Error {
  constructor(readonly code: "archive_bomb" | "corrupt_document" | "encrypted_rejected", message: string) {
    super(message);
    this.name = "ArchiveError";
  }
}

/** Decompress entries sequentially, enforcing both declared and actual byte limits. */
export async function readArchive(bytes: Buffer): Promise<Map<string, Buffer>> {
  const zip = await new Promise<ZipFile>((resolve, reject) => {
    fromBuffer(bytes, { lazyEntries: true, autoClose: false, validateEntrySizes: true, strictFileNames: true }, (error, file) => {
      if (error || !file) reject(new ArchiveError("corrupt_document", "invalid ZIP archive"));
      else resolve(file);
    });
  });
  const result = new Map<string, Buffer>();
  let entries = 0;
  let total = 0;
  try {
    await new Promise<void>((resolve, reject) => {
      let settled = false;
      const fail = (error: unknown) => {
        if (settled) return;
        settled = true;
        reject(error instanceof ArchiveError ? error : new ArchiveError("corrupt_document", "invalid ZIP entry"));
      };
      zip.on("error", fail);
      zip.on("end", () => { if (!settled) { settled = true; resolve(); } });
      zip.on("entry", (entry: Entry) => {
        entries += 1;
        if (entries > MAX_ENTRIES || entry.uncompressedSize > MAX_EXPANDED_BYTES || total + entry.uncompressedSize > MAX_EXPANDED_BYTES ||
            (entry.compressedSize > 0 && entry.uncompressedSize / entry.compressedSize > MAX_RATIO)) {
          fail(new ArchiveError("archive_bomb", "archive exceeds size, entry, or compression limits"));
          return;
        }
        if (entry.isEncrypted()) { fail(new ArchiveError("encrypted_rejected", "encrypted entries are not supported")); return; }
        if (result.has(entry.fileName)) { fail(new ArchiveError("corrupt_document", "duplicate archive entry")); return; }
        if (entry.fileName.endsWith("/")) { zip.readEntry(); return; }
        zip.openReadStream(entry, (error, stream) => {
          if (error || !stream) { fail(error); return; }
          const chunks: Buffer[] = [];
          let length = 0;
          stream.on("data", (chunk: Buffer) => {
            length += chunk.length;
            if (length > entry.uncompressedSize || total + length > MAX_EXPANDED_BYTES) {
              stream.destroy();
              fail(new ArchiveError("archive_bomb", "expanded archive exceeded the byte limit"));
            } else chunks.push(chunk);
          });
          stream.on("error", fail);
          stream.on("end", () => {
            if (settled) return;
            if (length !== entry.uncompressedSize) { fail(new ArchiveError("corrupt_document", "ZIP entry size mismatch")); return; }
            total += length;
            result.set(entry.fileName, Buffer.concat(chunks, length));
            zip.readEntry();
          });
        });
      });
      zip.readEntry();
    });
  } finally {
    zip.close();
  }
  return result;
}
