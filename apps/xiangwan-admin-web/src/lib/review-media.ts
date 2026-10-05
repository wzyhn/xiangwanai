export type ReviewMediaKind = "photo" | "video" | "audio" | "material";

export type ReviewMediaChoice = {
  kind: ReviewMediaKind;
  mime: string;
  maxBytes: number;
};

const choices: Record<string, ReviewMediaChoice> = {
  jpg: { kind: "photo", mime: "image/jpeg", maxBytes: 10 << 20 },
  jpeg: { kind: "photo", mime: "image/jpeg", maxBytes: 10 << 20 },
  png: { kind: "photo", mime: "image/png", maxBytes: 10 << 20 },
  webp: { kind: "photo", mime: "image/webp", maxBytes: 10 << 20 },
  mp4: { kind: "video", mime: "video/mp4", maxBytes: 200 << 20 },
  mp3: { kind: "audio", mime: "audio/mpeg", maxBytes: 50 << 20 },
  pdf: { kind: "material", mime: "application/pdf", maxBytes: 20 << 20 },
};

export function reviewMediaChoice(name: string, browserMIME: string, size: number): ReviewMediaChoice | null {
  const extension = name.split(".").pop()?.toLowerCase() || "";
  const choice = choices[extension];
  if (!choice || !Number.isSafeInteger(size) || size <= 0 || size > choice.maxBytes) return null;
  if (browserMIME && browserMIME !== choice.mime &&
    !(choice.mime === "audio/mpeg" && browserMIME === "audio/mp3")) return null;
  return choice;
}

export async function reviewMediaSHA256(file: Blob): Promise<string> {
  if (!globalThis.crypto?.subtle) throw new Error("当前浏览器无法校验文件摘要，请使用安全的 HTTPS 环境");
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
