import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "享玩 AI · 运营后台",
  description: "享玩 AI 独立活动运营与现场签到后台",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
