"use client";

type Envelope<T> = {
  code: number;
  message: string;
  data?: T;
};

type PageEnvelope<T> = {
  items: T[];
  page: number;
  page_size: number;
  total: number;
};

export class ApiError extends Error {
  readonly status: number;
  readonly code: number;
  readonly data: unknown;

  constructor(status: number, body: Envelope<unknown>) {
    super(body.message || "请求失败");
    this.name = "ApiError";
    this.status = status;
    this.code = body.code;
    this.data = body.data;
  }
}

const csrfCookieName = "__Host-xiangwan_admin_csrf";
const adminSessionInvalidCode = 30602;

function csrfToken(): string {
  if (typeof document === "undefined") return "";
  for (const pair of document.cookie.split(";")) {
    const [rawName, ...rawValue] = pair.trim().split("=");
    if (rawName === csrfCookieName) return decodeURIComponent(rawValue.join("="));
  }
  return "";
}

function isUnsafe(method: string): boolean {
  return !["GET", "HEAD", "OPTIONS"].includes(method.toUpperCase());
}

function redirectExpiredAdminSession(): void {
  if (typeof window === "undefined" || window.location.pathname === "/login") return;
  const currentPath = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  const returnTo = currentPath.length <= 512 ? currentPath : "/activities";
  window.location.replace(
    `/login?reason=session_expired&return_to=${encodeURIComponent(returnTo)}`,
  );
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = init.method || "GET";
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (isUnsafe(method)) {
    headers.set("Content-Type", "application/json");
    const csrf = csrfToken();
    if (csrf) headers.set("X-Xiangwan-CSRF-Token", csrf);
  }
  const response = await fetch(`/api/v1/xiangwan/admin${path}`, {
    ...init,
    method,
    headers,
    credentials: "include",
    cache: "no-store",
  });
  let body: Envelope<T>;
  try {
    body = (await response.json()) as Envelope<T>;
  } catch {
    throw new Error("服务返回了无法识别的响应");
  }
  if (!response.ok || body.code !== 0 || body.data === undefined) {
    const error = new ApiError(response.status, body as Envelope<unknown>);
    if (error.status === 401 && error.code === adminSessionInvalidCode) {
      redirectExpiredAdminSession();
    }
    throw error;
  }
  return body.data;
}

// apiDownload keeps binary/text exports on the same authenticated admin
// session. The response is kept in browser memory only; callers decide when
// to create a temporary object URL and never persist export data.
export async function apiDownload(path: string): Promise<Blob> {
  const headers = new Headers({ Accept: "text/csv, application/json" });
  const csrf = csrfToken();
  if (csrf) headers.set("X-Xiangwan-CSRF-Token", csrf);
  const response = await fetch(`/api/v1/xiangwan/admin${path}`, {
    method: "GET",
    headers,
    credentials: "include",
    cache: "no-store",
  });
  if (!response.ok) {
    let body: Envelope<unknown> = { code: response.status, message: "请求失败" };
    try {
      body = (await response.json()) as Envelope<unknown>;
    } catch {
      // Keep the status-based error below when an upstream response is not JSON.
    }
    const error = new ApiError(response.status, body);
    if (error.status === 401 && error.code === adminSessionInvalidCode) {
      redirectExpiredAdminSession();
    }
    throw error;
  }
  return response.blob();
}

// apiUpload sends multipart form data with the same session, CSRF, and
// envelope handling as api(). The browser sets the multipart boundary, so no
// Content-Type is assigned here.
export async function apiUpload<T>(path: string, form: FormData): Promise<T> {
  const headers = new Headers();
  headers.set("Accept", "application/json");
  const csrf = csrfToken();
  if (csrf) headers.set("X-Xiangwan-CSRF-Token", csrf);
  const response = await fetch(`/api/v1/xiangwan/admin${path}`, {
    method: "POST",
    headers,
    body: form,
    credentials: "include",
    cache: "no-store",
  });
  let body: Envelope<T>;
  try {
    body = (await response.json()) as Envelope<T>;
  } catch {
    throw new Error("服务返回了无法识别的响应");
  }
  if (!response.ok || body.code !== 0 || body.data === undefined) {
    const error = new ApiError(response.status, body as Envelope<unknown>);
    if (error.status === 401 && error.code === adminSessionInvalidCode) {
      redirectExpiredAdminSession();
    }
    throw error;
  }
  return body.data;
}

// Exact-byte review uploads use a private PUT intent route. The request body
// is the browser File itself; no filename or arbitrary metadata is submitted.
export async function apiBinaryUpload<T>(path: string, file: Blob): Promise<T> {
  const headers = new Headers({
    Accept: "application/json",
    "Content-Type": "application/octet-stream",
  });
  const csrf = csrfToken();
  if (csrf) headers.set("X-Xiangwan-CSRF-Token", csrf);
  const response = await fetch(`/api/v1/xiangwan/admin${path}`, {
    method: "PUT",
    headers,
    body: file,
    credentials: "include",
    cache: "no-store",
  });
  let body: Envelope<T>;
  try {
    body = (await response.json()) as Envelope<T>;
  } catch {
    throw new Error("服务返回了无法识别的响应");
  }
  if (!response.ok || body.code !== 0 || body.data === undefined) {
    const error = new ApiError(response.status, body as Envelope<unknown>);
    if (error.status === 401 && error.code === adminSessionInvalidCode) {
      redirectExpiredAdminSession();
    }
    throw error;
  }
  return body.data;
}

export async function allPages<T>(path: string, init: RequestInit = {}): Promise<{ items: T[]; total: number }> {
  const pageSize = 100;
  const separator = path.includes("?") ? "&" : "?";
  const first = await api<PageEnvelope<T>>(`${path}${separator}page=1&page_size=${pageSize}`, init);
  const items = [...first.items];
  let total = first.total;
  let page = 2;
  while (items.length < total) {
    const next = await api<PageEnvelope<T>>(`${path}${separator}page=${page}&page_size=${pageSize}`, init);
    if (next.items.length === 0) throw new Error("服务返回了不完整的分页结果");
    items.push(...next.items);
    total = Math.max(total, next.total);
    page += 1;
  }
  return { items, total };
}

export function operationKey(): string {
  return crypto.randomUUID();
}

export function operationHeaders(key: string): HeadersInit {
  return { "Idempotency-Key": key };
}

export function displayError(error: unknown): string {
  if (error instanceof ApiError) {
    const fallbackByStatus: Record<number, string> = {
      400: "提交内容有误，请检查后重试",
      401: "登录状态已过期，请重新登录",
      403: "当前账号没有此操作权限",
      404: "相关内容不存在或已下线",
      409: "内容已更新，请刷新后重试",
      429: "操作过于频繁，请稍后再试",
    };
    const technical = /\b(postgresql|redis|oidc|pkce|principal|series|instance|session|registration|checkin|coupon|order|idempotency|runtime|token|api|grant|generation)\b|internal server error|service unavailable|invalid|failed|not found|conflict|unavailable/i;
    if (/[\u3400-\u9fff]/u.test(error.message) && !technical.test(error.message)) {
      return error.message;
    }
    return fallbackByStatus[error.status] || "服务暂时开小差，请稍后重试";
  }
  if (error instanceof Error && /[\u3400-\u9fff]/u.test(error.message)) return error.message;
  return "请求失败，请稍后重试";
}

export function definitiveFailure(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status >= 400 &&
    error.status < 500 &&
    ![408, 425, 429].includes(error.status)
  );
}
