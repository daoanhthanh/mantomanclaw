import assert from "node:assert/strict";
import test from "node:test";
import JSZip from "jszip";
import { extractDocument } from "../src/documents.js";

test("extracts docx headings paragraphs tables and rejects unsafe files", async () => {
  const docx = await zip({
    "word/document.xml": `<?xml version="1.0"?><w:document><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Scope</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Must ship</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`,
  });
  const extracted = await extractDocument("req.docx", docx);
  assert.match(extracted.text, /Scope/);
  assert.match(extracted.text, /Must ship/);
  assert.ok(extracted.sources.some((source) => source.includes("table1")));
  await assert.rejects(extractDocument("old.doc", Buffer.from([0xd0, 0xcf, 0x11, 0xe0, 0, 0])), /legacy/);
  const macro = await zip({ "word/document.xml": "<w:document/>", "word/vbaProject.bin": "nope" });
  await assert.rejects(extractDocument("macro.docx", macro), /macro/);
  const bomb = new JSZip();
  bomb.file("word/document.xml", "x".repeat(1024 * 1024));
  const compressed = await bomb.generateAsync({ type: "nodebuffer", compression: "DEFLATE" });
  await assert.rejects(extractDocument("bomb.docx", compressed), (error: unknown) => error instanceof Error && "code" in error && error.code === "archive_bomb");
});

test("document extraction reports output truncated at the byte limit", async () => {
  const bytes = await zip({ "word/document.xml": `<w:document><w:p><w:t>${"a".repeat(100_000)}</w:t></w:p></w:document>` });
  const result = await extractDocument("large.docx", bytes);
  assert.equal(result.truncated, true);
  assert.match(result.coverage, /truncated=true/);
  assert.ok(Buffer.byteLength(result.text) <= 96 * 1024);
});

test("flags xlsx formulas without cached values and keeps cell labels", async () => {
  const workbook = `<?xml version="1.0"?><workbook><sheet name="Scope" r:id="rId2"/></workbook>`;
  const relationships = `<Relationships><Relationship Id="rId2" Target="worksheets/sheet9.xml"/></Relationships>`;
  const sheet = `<?xml version="1.0"?><worksheet><sheetData><row><c r="B12" t="inlineStr"><is><t>Ready</t></is></c><c r="C12"><f>SUM(A1:A2)</f></c><c r="D12"><v>42</v></c></row></sheetData></worksheet>`;
  const bytes = await zip({ "xl/workbook.xml": workbook, "xl/_rels/workbook.xml.rels": relationships, "xl/worksheets/sheet9.xml": sheet });
  const extracted = await extractDocument("req.xlsx", bytes);
  assert.match(extracted.text, /xlsx:req.xlsx:Scope!B12/);
  assert.match(extracted.text, /Scope!D12\] 42/);
  assert.ok(extracted.warnings.some((warning) => warning.includes("no cached result")));
});

async function zip(files: Record<string, string>): Promise<Buffer> {
  const archive = new JSZip();
  for (const [name, contents] of Object.entries(files)) archive.file(name, contents);
  return Buffer.from(await archive.generateAsync({ type: "uint8array" }));
}
