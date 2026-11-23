import { posix } from "node:path";
import { readArchive } from "./archive.js";

const MAX_COMPRESSED = 8 * 1024 * 1024;
const MAX_ROWS = 200;
const MAX_CELLS = 2000;
const MAX_PARAGRAPHS = 500;

export type Extraction = {
  text: string;
  sources: string[];
  warnings: string[];
  truncated: boolean;
  coverage: string;
};

export class DocumentError extends Error {
  constructor(readonly code: string, message: string) {
    super(message);
    this.name = "DocumentError";
  }
}

export async function extractDocument(name: string, bytes: Buffer): Promise<Extraction> {
  if (bytes.length > MAX_COMPRESSED) throw new DocumentError("document_too_large", "compressed file exceeds the byte cap");
  if (isOle(bytes)) throw new DocumentError("legacy_rejected", "legacy DOC/XLS files are rejected");
  const lower = name.toLowerCase();
  if (lower.endsWith(".doc") || lower.endsWith(".xls")) throw new DocumentError("legacy_rejected", "legacy DOC/XLS files are rejected");
  const zip = await readArchive(bytes);
  const names = [...zip.keys()];
  if (names.some((entry) => entry.toLowerCase().endsWith("vbaproject.bin"))) {
    throw new DocumentError("macro_rejected", "macro projects are rejected and never executed");
  }
  if (names.some((entry) => entry.toLowerCase().includes("encryptioninfo"))) {
    throw new DocumentError("encrypted_rejected", "encrypted office files are rejected");
  }
  if (names.includes("word/document.xml")) return extractDocx(name, zip);
  if (names.includes("xl/workbook.xml")) return extractXlsx(name, zip);
  throw new DocumentError("unsupported_document", "only DOCX and XLSX are accepted");
}


async function extractDocx(name: string, zip: Map<string, Buffer>): Promise<Extraction> {
  const xml = zip.get("word/document.xml")?.toString("utf8");
  if (!xml) throw new DocumentError("corrupt_document", "DOCX is missing document.xml");
  const warnings: string[] = [];
  const sources: string[] = [];
  const chunks: string[] = [];
  let truncated = false;
  let outputBytes = 0;
  let outputFull = false;
  const tables = extractRanges(xml, "w:tbl");
  let tableIndex = 0;
  for (const table of tables) {
    tableIndex += 1;
    const rows = extractRanges(table, "w:tr").slice(0, MAX_ROWS);
    if (extractRanges(table, "w:tr").length > MAX_ROWS) {
      warnings.push(`docx table ${tableIndex} row cap reached`);
      truncated = true;
    }
    rows.forEach((row, rowIndex) => {
      const cells = extractRanges(row, "w:tc");
      cells.forEach((cell, cellIndex) => {
        if (outputFull) return;
        const label = `docx:${name}:table${tableIndex}.r${rowIndex + 1}.c${cellIndex + 1}`;
        const text = textOf(cell);
        if (!text) return;
        const size = Buffer.byteLength(label) + Buffer.byteLength(text) + 4;
        if (outputBytes + size > 96 * 1024) {
          outputFull = true;
          truncated = true;
          warnings.push("extracted text byte cap reached");
          return;
        }
        outputBytes += size;
        sources.push(label);
        chunks.push(`[${label}] ${text}`);
      });
    });
  }
  const outside = removeRanges(xml, "w:tbl");
  const paragraphs = extractRanges(outside, "w:p").slice(0, MAX_PARAGRAPHS);
  if (extractRanges(outside, "w:p").length > MAX_PARAGRAPHS) {
    warnings.push("docx paragraph cap reached");
    truncated = true;
  }
  paragraphs.forEach((paragraph, index) => {
    if (outputFull) return;
    const text = textOf(paragraph);
    if (!text) return;
    const heading = /w:val="Heading/i.test(paragraph);
    const label = `docx:${name}:${heading ? "h" : "p"}${index + 1}`;
    const size = Buffer.byteLength(label) + Buffer.byteLength(text) + 6;
    if (outputBytes + size > 96 * 1024) {
      outputFull = true;
      truncated = true;
      warnings.push("extracted text byte cap reached");
      return;
    }
    outputBytes += size;
    sources.push(label);
    chunks.push(`[${label}] ${heading ? "# " : ""}${text}`);
  });
  return finish(name, chunks, sources, warnings, truncated);
}

async function extractXlsx(name: string, zip: Map<string, Buffer>): Promise<Extraction> {
  const workbook = zip.get("xl/workbook.xml")?.toString("utf8");
  if (!workbook) throw new DocumentError("corrupt_document", "XLSX is missing workbook.xml");
  const shared = zip.get("xl/sharedStrings.xml")?.toString("utf8");
  const strings = shared ? extractRanges(shared, "si").map((item) => textOf(item)) : [];
  const relationships = zip.get("xl/_rels/workbook.xml.rels")?.toString("utf8");
  if (!relationships) throw new DocumentError("corrupt_document", "workbook relationships are missing");
  const targets = new Map<string, string>();
  for (const match of relationships.matchAll(/<Relationship\b[^>]*\/?>/g)) {
    const id = attr(match[0], "Id");
    const target = attr(match[0], "Target");
    if (id && target && !attr(match[0], "TargetMode")) targets.set(id, target);
  }
  const sheets = [...workbook.matchAll(/<sheet\b[^>]*\/?>/g)].map((match) => ({
    name: attr(match[0], "name"),
    relationship: attr(match[0], "r:id"),
  }));
  if (sheets.length === 0) throw new DocumentError("corrupt_document", "workbook contains no readable sheets");
  const warnings: string[] = [];
  const sources: string[] = [];
  const chunks: string[] = [];
  let cells = 0;
  let truncated = false;
  let outputBytes = 0;
  for (const sheet of sheets) {
    if (!sheet.name || !sheet.relationship) throw new DocumentError("corrupt_document", "worksheet identity is missing");
    const sheetName = decodeXml(sheet.name);
    const target = targets.get(sheet.relationship);
    if (!target) throw new DocumentError("corrupt_document", `worksheet relationship missing for ${sheetName}`);
    const path = target.startsWith("/") ? posix.normalize(target.slice(1)) : posix.normalize(posix.join("xl", target));
    if (!path.startsWith("xl/worksheets/") || path.includes("../")) throw new DocumentError("corrupt_document", "worksheet target is outside the workbook");
    const xml = zip.get(path)?.toString("utf8");
    if (!xml) throw new DocumentError("corrupt_document", `worksheet content missing for ${sheetName}`);
    const rows = extractRanges(xml, "row");
    if (rows.length > MAX_ROWS) {
      warnings.push(`${sheetName} row cap reached`);
      truncated = true;
    }
    for (const row of rows.slice(0, MAX_ROWS)) {
      for (const cell of extractRanges(row, "c")) {
        cells += 1;
        if (cells > MAX_CELLS) {
          warnings.push("cell cap reached");
          truncated = true;
          break;
        }
        const ref = attr(cell, "r") ?? "?";
        const formula = extractRanges(cell, "f")[0];
        const cached = extractRanges(cell, "v")[0];
        if (formula && !cached) warnings.push(`${sheetName}!${ref} formula has no cached result`);
        const value = cellValue(cell, strings);
        if (!value) continue;
        const label = `xlsx:${name}:${sheetName}!${ref}`;
        const size = Buffer.byteLength(label) + Buffer.byteLength(value) + 4;
        if (outputBytes + size > 96 * 1024) {
          warnings.push("extracted text byte cap reached");
          return finish(name, chunks, sources, warnings, true);
        }
        outputBytes += size;
        sources.push(label);
        chunks.push(`[${label}] ${value}`);
      }
    }
  }
  return finish(name, chunks, sources, warnings, truncated);
}

function cellValue(cell: string, strings: string[]): string {
  const type = attr(cell, "t");
  const valueElement = extractRanges(cell, "v")[0] ?? "";
  const raw = decodeXml(valueElement.replace(/^<v(?:\s[^>]*)?>/, "").replace(/<\/v>$/, "")).trim();
  if (type === "s") return strings[Number(raw)] ?? "";
  if (type === "inlineStr") return textOf(cell);
  return raw;
}

function finish(name: string, chunks: string[], sources: string[], warnings: string[], truncated: boolean): Extraction {
  const accepted: string[] = [];
  let length = 0;
  for (const chunk of chunks) {
    const size = Buffer.byteLength(chunk) + (accepted.length ? 1 : 0);
    if (length + size > 96 * 1024) {
      truncated = true;
      warnings.push("extracted text byte cap reached");
      break;
    }
    accepted.push(chunk);
    length += size;
  }
  const includedSources = sources.slice(0, accepted.length);
  const coverage = `${includedSources.length}/${sources.length} extracted source labels from ${name}; truncated=${truncated}`;
  return {
    text: accepted.join("\n"),
    sources: includedSources,
    warnings,
    truncated,
    coverage,
  };
}

function textOf(xml: string): string {
  return [...xml.matchAll(/<w:t[^>]*>([^<]*)<\/w:t>|<t[^>]*>([^<]*)<\/t>/g)]
    .map((match) => decodeXml(match[1] ?? match[2] ?? ""))
    .join("")
    .replace(/\s+/g, " ")
    .trim();
}

function extractRanges(xml: string, tag: string): string[] {
  const out: string[] = [];
  const open = `<${tag}`;
  let cursor = 0;
  while (cursor < xml.length) {
    const start = xml.indexOf(open, cursor);
    if (start < 0) break;
    const end = xml.indexOf(`</${tag}>`, start);
    if (end < 0) break;
    out.push(xml.slice(start, end + tag.length + 3));
    cursor = end + tag.length + 3;
  }
  return out;
}

function removeRanges(xml: string, tag: string): string {
  let out = xml;
  for (const range of extractRanges(xml, tag)) out = out.replace(range, "");
  return out;
}

function attr(xml: string, name: string): string | undefined {
  const match = xml.match(new RegExp(`\\b${name}="([^"]+)"`));
  return match?.[1];
}

function decodeXml(value: string): string {
  return value.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&").replace(/&quot;/g, '"').replace(/&apos;/g, "'");
}

function isOle(bytes: Buffer): boolean {
  return bytes.length >= 4 && bytes[0] === 0xd0 && bytes[1] === 0xcf && bytes[2] === 0x11 && bytes[3] === 0xe0;
}
