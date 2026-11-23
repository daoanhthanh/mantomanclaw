import { readFile, writeFile } from "node:fs/promises";
import JSZip from "jszip";

const appId = required("TEAMS_APP_ID");
const botId = required("TEAMS_BOT_ID");
const host = required("TEAMS_APP_HOST");
if (!/^[0-9a-f-]{36}$/i.test(appId) || !/^[0-9a-f-]{36}$/i.test(botId)) throw new Error("app and bot IDs must be UUIDs");
if (!/^[a-z0-9.-]+$/i.test(host) || !host.includes(".")) throw new Error("TEAMS_APP_HOST must be a hostname");
const template = await readFile(new URL("../appPackage/manifest.template.json", import.meta.url), "utf8");
const manifest = template.replaceAll("${TEAMS_APP_ID}", appId).replaceAll("${TEAMS_BOT_ID}", botId).replaceAll("${TEAMS_APP_HOST}", host);
JSON.parse(manifest);
const zip = new JSZip();
zip.file("manifest.json", manifest);
for (const name of ["color.png", "outline.png"]) zip.file(name, await readFile(new URL(`../appPackage/${name}`, import.meta.url)));
const output = process.env.TEAMS_APP_PACKAGE ?? "teams-app.zip";
await writeFile(output, await zip.generateAsync({ type: "nodebuffer" }));
console.log(`Wrote ${output}`);

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}
