
import { translate } from "@/i18n";export const GENERATED_TRAILER = "Nipa-Generated";

export function addTrailer(text: string, key: string, value: string): string {
  const v = value.trim();
  if (v === "") return text;
  if (text.split("\n").some((line) => line.trim().startsWith(`${key}:`))) return text;
  const body = text.replace(/\n+$/, "");
  return body === "" ? translate("generate.text", { key, v }) : translate("generate.text2", { body, key, v });
}

export function withGeneratedTrailer(text: string, model: string | null): string {
  return model && text.trim() !== "" ? addTrailer(text, GENERATED_TRAILER, model) : text;
}
