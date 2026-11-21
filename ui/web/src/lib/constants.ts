// Barrel re-exports for backward compatibility.
// Import directly from sub-modules for new code.
export { ROUTES } from "./routes";
export {
  TIMEZONE_OPTIONS,
  getAllIanaTimezones,
  isValidIanaTimezone,
} from "./timezone-utils";

export const LOCAL_STORAGE_KEYS = {
  TOKEN: "magent:token",
  USER_ID: "magent:userId",
  SENDER_ID: "magent:senderID",
  TENANT_ID: "magent:tenant_id",
  TENANT_HINT: "magent:tenant_hint",
  SETUP_SKIPPED: "magent:setup_skipped",
  THEME: "magent:theme",
  SIDEBAR_COLLAPSED: "magent:sidebarCollapsed",
  LANGUAGE: "magent:language",
  TIMEZONE: "magent:timezone",
} as const;

export const SUPPORTED_LANGUAGES = ["en", "vi", "zh", "ko", "ru"] as const;
export type Language = (typeof SUPPORTED_LANGUAGES)[number];

export const LANGUAGE_LABELS: Record<Language, string> = {
  en: "English",
  vi: "Tiếng Việt",
  zh: "中文",
  ko: "한국어",
  ru: "Русский",
};
