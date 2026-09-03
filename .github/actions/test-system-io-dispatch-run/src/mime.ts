import * as path from "node:path";

/** MIME type for orchestration/report screenshot uploads (png/jpeg only). */
export function screenshotContentType(filePath: string): string {
  return imageContentType(filePath) ?? "image/png";
}

/** image/png or image/jpeg for known screenshot extensions; otherwise null. */
export function imageContentType(filePath: string): string | null {
  const ext = path.extname(filePath).toLowerCase();
  if (ext === ".png") return "image/png";
  if (ext === ".jpg" || ext === ".jpeg") return "image/jpeg";
  return null;
}
