"use client";

import { ArrowRight, ShieldCheck } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useMemo, useState } from "react";
import { api, displayError } from "@/lib/api";
import { canOpenAdminPath, defaultAdminPath } from "@/lib/admin-permissions";
import type { AuthStatus } from "@/lib/types";

const reasons: Record<string, string> = {
  identity_rejected: "当前账号尚未开通运营权限，请联系管理员。",
  login_expired: "登录请求已过期，请重新发起登录。",
  session_expired: "管理会话已过期，请重新登录后继续。",
};

function LoginContent() {
  const query = useSearchParams();
  const router = useRouter();
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [error, setError] = useState("");
  const returnTo = useMemo(() => {
    const value = query.get("return_to") || "/activities";
    // Mirrors the server ValidReturnTo rule. A backslash is normalized to a
    // slash during navigation, so "/\host" would otherwise leave the origin.
    const local =
      value.startsWith("/") &&
      !value.startsWith("//") &&
      !value.includes("\\") &&
      !value.includes("\r") &&
      !value.includes("\n") &&
      !value.includes("\0") &&
      value.length <= 512;
    return local ? value : "/activities";
  }, [query]);

  useEffect(() => {
    let active = true;
    void api<AuthStatus>("/auth/status")
      .then((value) => {
        if (!active) return;
        if (value.authenticated) {
          router.replace(canOpenAdminPath(value, returnTo) ? returnTo : defaultAdminPath(value));
          return;
        }
        setStatus(value);
      })
      .catch((reason: unknown) => active && setError(displayError(reason)));
    return () => {
      active = false;
    };
  }, [returnTo, router]);

  const reason = query.get("reason");
  const message = error || (reason ? reasons[reason] : "");

  return (
    <main className="login-page">
      <section className="login-brand">
        <div className="login-lockup"><span className="brand-mark large">享</span><b>享玩 AI</b></div>
        <div>
          <span className="eyebrow">享玩活动运营</span>
          <h1>把每一次相聚，安排得清楚又从容。</h1>
          <p>从活动发布到现场签到，一个后台完成日常运营。</p>
        </div>
        <small>活动发布 · 报名管理 · 现场签到</small>
      </section>
      <section className="login-panel">
        <div className="login-card">
          <span className="security-icon"><ShieldCheck /></span>
          <h2>运营人员登录</h2>
          <p>使用客户统一身份进入。享玩不会保存身份提供商的访问令牌。</p>
          {message && <div className="inline-message error">{message}</div>}
          {status === null && !error ? (
            <button className="primary-button wide" disabled>正在检查登录服务…</button>
          ) : status?.enabled ? (
            <a className="primary-button wide" href={`/api/v1/xiangwan/admin/auth/login?return_to=${encodeURIComponent(returnTo)}`}>
              使用统一身份登录 <ArrowRight />
            </a>
          ) : (
            <div className="inline-message warning">登录服务尚未开通，请联系系统管理员。</div>
          )}
          <div className="trust-list">
            <span>统一身份登录</span>
            <span>登录状态安全保护</span>
            <span>按岗位分配权限</span>
          </div>
        </div>
      </section>
    </main>
  );
}

export default function LoginPage() {
  return <Suspense fallback={<main className="center-state"><div className="loading-dot" /></main>}><LoginContent /></Suspense>;
}
