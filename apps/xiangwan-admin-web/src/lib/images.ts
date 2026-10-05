"use client";

import { apiUpload } from "@/lib/api";

export const activityImageAccept = ["image/jpeg", "image/png", "image/webp"];
export const activityImageMaxBytes = 5 * 1024 * 1024;

// 活动封面与图文详情图片同一条上传链路:服务端只接受 JPEG/PNG/WebP(魔数校验)、
// 5 MiB 以内,返回站内相对路径(/api/v1/xiangwan/covers/<32hex>.<ext>)。
// 表单里保存相对路径或 https 绝对 URL 均可;预览时 resolveActivityImageUrl
// 把站内相对路径原样交给浏览器,由同源 Next rewrite(next.config.mjs)代理到
// API_ORIGIN——dev 下管理台是 https、后端是 http,拼绝对 http 地址会触发
// mixed-content 并被 CSP img-src 拦截,https 绝对 URL 则原样返回。
const coverPathPattern = /^\/api\/v1\/xiangwan\/covers\/[0-9a-f]{32}\.[a-z0-9]+$/i;

export function isActivityImageRef(value: string): boolean {
  const trimmed = value.trim();
  if (coverPathPattern.test(trimmed)) return true;
  try {
    const url = new URL(trimmed);
    return url.protocol === "https:" && !url.username && !url.password && !url.hash;
  } catch {
    return false;
  }
}

export function resolveActivityImageUrl(value: string): string {
  return value;
}

export function validateActivityImage(file: File): string {
  if (!activityImageAccept.includes(file.type)) return "图片仅支持 JPG、PNG 或 WebP 格式。";
  if (file.size > activityImageMaxBytes) return "图片不能超过 5 MiB。";
  return "";
}

export async function uploadActivityImage(file: File): Promise<string> {
  const payload = new FormData();
  payload.append("image", file);
  const result = await apiUpload<{ url: string }>("/cover-images", payload);
  return result.url;
}
