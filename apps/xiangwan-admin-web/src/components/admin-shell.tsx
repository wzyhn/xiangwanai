"use client";

import {
  CalendarDays,
  CircleDollarSign,
  ClipboardList,
  History,
  LayoutDashboard,
  Image as ImageIcon,
  LogOut,
  Menu,
  QrCode,
  Sparkles,
  Users,
  X,
} from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { api, displayError } from "@/lib/api";
import {
  canCheckin,
  canOpenAdminPath,
  canOperateActivities,
  canReadAuditLog,
  canReadFinance,
  canReadOrders,
  canReadRegistrations,
  defaultAdminPath,
} from "@/lib/admin-permissions";
import type { AuthStatus } from "@/lib/types";

const navigation = [
  { href: "/brand", label: "首页配置", icon: ImageIcon },
  { href: "/activities", label: "活动期次", icon: CalendarDays },
  { href: "/series", label: "系列管理", icon: CalendarDays },
  { href: "/past-activities", label: "往期活动", icon: History },
  { href: "/questionnaire-templates", label: "报名模板", icon: ClipboardList },
  { href: "/registrations", label: "报名管理", icon: ClipboardList },
  { href: "/checkin-live", label: "签到核销", icon: QrCode },
  { href: "/people", label: "人物角色", icon: Users },
  { href: "/audit-events", label: "审计日志", icon: ClipboardList },
  { href: "/orders", label: "订单查询", icon: CircleDollarSign },
  { href: "/refunds", label: "退款待办", icon: CircleDollarSign },
  { href: "/coupon-corrections", label: "优惠券异常", icon: ClipboardList },
];

const pageCopy = [
 {prefix:"/host-applications",title:"主理人申请",subtitle:"发布规则并审核申请"},
  { prefix: "/brand", title: "首页配置", subtitle: "发布小程序品牌区与热门主题" },
  { prefix: "/activities/new", title: "发布新一期", subtitle: "建立活动、场次并完成发布" },
  { prefix: "/activities/", title: "活动详情", subtitle: "核对场次与发布状态" },
  { prefix: "/activities", title: "活动期次", subtitle: "管理系列、期次和小程序展示" },
  { prefix: "/series", title: "系列管理", subtitle: "维护长期栏目与首页曝光" },
  { prefix: "/past-activities", title: "往期活动", subtitle: "查看系列全部期次与公开回顾入口" },
  { prefix: "/questionnaire-templates", title: "报名模板", subtitle: "复用问卷字段并生成独立期次快照" },
  { prefix: "/people", title: "人物角色", subtitle: "维护活动人物资料与角色绑定" },
  { prefix: "/audit-events", title: "审计日志", subtitle: "核查后台操作与敏感读取记录" },
  { prefix: "/coupon-corrections", title: "优惠券异常", subtitle: "查看签到撤销后的权益复核线索" },
  { prefix: "/orders/", title: "订单详情", subtitle: "核对价格快照与支付事实" },
  { prefix: "/orders", title: "订单查询", subtitle: "核对订单支付与退款状态" },
  { prefix: "/refunds/", title: "退款详情", subtitle: "核对退款状态和处理时间线" },
  { prefix: "/refunds", title: "退款待办", subtitle: "查看人工退款处理队列" },
  { prefix: "/registrations/", title: "报名详情", subtitle: "查看报名进度与参与情况" },
  { prefix: "/registrations", title: "报名管理", subtitle: "按场次查看和筛选报名" },
  { prefix: "/checkin-live", title: "签到核销", subtitle: "扫码或输入备份码完成签到" },
  { prefix: "/checkin", title: "签到核销", subtitle: "扫码或输入备份码完成签到" },
];

const capabilityLabels: Record<string, string> = {
  super_admin: "系统管理员",
  activity_operator: "活动运营",
  onsite_checkin: "现场签到",
  finance: "财务核对",
};

export function AdminShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [error, setError] = useState("");
  const [menuOpen, setMenuOpen] = useState(false);

  useEffect(() => {
    let active = true;
    void api<AuthStatus>("/auth/status")
      .then((status) => {
        if (!active) return;
        if (!status.enabled || !status.authenticated) {
          const target = encodeURIComponent(pathname || "/activities");
          router.replace(`/login?return_to=${target}`);
          return;
        }
        if (!canOpenAdminPath(status, pathname)) {
          router.replace(defaultAdminPath(status));
          return;
        }
        setAuth(status);
      })
      .catch((reason: unknown) => active && setError(displayError(reason)));
    return () => {
      active = false;
    };
  }, [pathname, router]);

  const copy = useMemo(
    () => pageCopy.find((item) => pathname.startsWith(item.prefix)) || pageCopy[3],
    [pathname],
  );
  const activityAccess = auth ? canOperateActivities(auth) : false;
  const registrationAccess = auth ? canReadRegistrations(auth) : false;
  const financeAccess = auth ? canReadFinance(auth) : false;
  const orderAccess = auth ? canReadOrders(auth) : false;
  const checkinAccess = auth ? canCheckin(auth) : false;
  const auditAccess = auth ? canReadAuditLog(auth) : false;

  async function logout() {
    setError("");
    try {
      await api<{ logged_out: boolean }>("/auth/logout", {
        method: "POST",
        body: "{}",
      });
      router.replace("/login");
    } catch (reason) {
      setError(displayError(reason));
    }
  }

  if (error) {
    return (
      <main className="center-state">
        <div className="state-card">
          <span className="brand-mark">享</span>
          <h1>后台暂时无法连接</h1>
          <p>{error}</p>
          <button className="primary-button" onClick={() => location.reload()}>重新加载</button>
        </div>
      </main>
    );
  }
  if (!auth) {
    return <main className="center-state"><div className="loading-dot" aria-label="正在验证登录" /></main>;
  }

  return (
    <main className="admin-layout">
      <aside className={`sidebar ${menuOpen ? "open" : ""}`}>
        <div className="admin-logo">
          <span className="brand-mark">享</span>
          <span><b>享玩 AI</b><small>运营管理后台</small></span>
          <button className="mobile-close" onClick={() => setMenuOpen(false)} aria-label="关闭菜单"><X /></button>
        </div>
        <nav>
          {activityAccess && <>
            <p>运营总览</p>
            <Link href="/activities" className={pathname.startsWith("/activities") ? "active" : ""}>
              <LayoutDashboard /> 活动概览
            </Link>
          </>}
          {activityAccess && <>
            <p>内容运营</p>
            <Link href={navigation[0].href} className={pathname.startsWith(navigation[0].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <ImageIcon /> {navigation[0].label}
            </Link>
            <p>活动运营</p>
            <Link href={navigation[1].href} className={pathname.startsWith(navigation[1].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <CalendarDays /> {navigation[1].label}
            </Link>
            <Link href={navigation[2].href} className={pathname.startsWith(navigation[2].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <CalendarDays /> {navigation[2].label}
            </Link>
            <Link href={navigation[3].href} className={pathname.startsWith(navigation[3].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <History /> {navigation[3].label}
            </Link>
            <Link href={navigation[4].href} className={pathname.startsWith(navigation[4].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <ClipboardList /> {navigation[4].label}
            </Link>
            <Link href={navigation[7].href} className={pathname.startsWith(navigation[7].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <Users /> {navigation[7].label}
            </Link>
          </>}
          {registrationAccess && <>
            <p>用户运营</p>
 {activityAccess&&<Link href="/host-applications" className={pathname.startsWith("/host-applications")?"active":""} onClick={()=>setMenuOpen(false)}><Users/>主理人申请</Link>}
            <Link href={navigation[5].href} className={pathname.startsWith(navigation[5].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <ClipboardList /> {navigation[5].label}
            </Link>
          </>}
          {(orderAccess || financeAccess) && <>
            <p>财务核对</p>
            {orderAccess && <Link href={navigation[9].href} className={pathname.startsWith(navigation[9].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <CircleDollarSign /> {navigation[9].label}
            </Link>}
            {financeAccess && <Link href={navigation[10].href} className={pathname.startsWith(navigation[10].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <CircleDollarSign /> {navigation[10].label}
            </Link>}
          </>}
          {checkinAccess && <>
            <p>现场</p>
            <Link href={navigation[6].href} className={pathname.startsWith(navigation[6].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <QrCode /> {navigation[6].label}
            </Link>
          </>}
          {auditAccess && <>
            <p>安全审计</p>
            <Link href={navigation[8].href} className={pathname.startsWith(navigation[8].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <ClipboardList /> {navigation[8].label}
            </Link>
            <Link href={navigation[11].href} className={pathname.startsWith(navigation[11].href) ? "active" : ""} onClick={() => setMenuOpen(false)}>
              <ClipboardList /> {navigation[11].label}
            </Link>
          </>}
        </nav>
        <div className="admin-user">
          <span className="avatar"><Sparkles /></span>
          <span><b>{activityAccess ? "社区运营" : financeAccess ? "财务人员" : "现场人员"}</b><small>{auth.grants.map((grant) => capabilityLabels[grant.capability] || "已授权").join(" · ")}</small></span>
          <button onClick={logout} aria-label="退出登录"><LogOut /></button>
        </div>
      </aside>
      {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} aria-label="关闭菜单" />}
      <section className="admin-main">
        <header className="admin-top">
          <button className="mobile-menu" onClick={() => setMenuOpen(true)} aria-label="打开菜单"><Menu /></button>
          <div><h1>{copy.title}</h1><p>{copy.subtitle}</p></div>
          <span className="secure-indicator">安全登录</span>
        </header>
        {children}
      </section>
    </main>
  );
}
