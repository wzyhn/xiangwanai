/** @type {import('next').NextConfig} */

const configuredAPIOrigin = process.env.XIANGWAN_ADMIN_API_ORIGIN;
if (process.env.NODE_ENV === "production" && !configuredAPIOrigin) {
  throw new Error("XIANGWAN_ADMIN_API_ORIGIN is required in production");
}
const API_ORIGIN = configuredAPIOrigin || "http://127.0.0.1:8082";
const developmentScriptSource = process.env.NODE_ENV === "development" ? " 'unsafe-eval'" : "";

let parsedOrigin;
try { parsedOrigin = new URL(API_ORIGIN); } catch { /* rejected below */ }
if (!parsedOrigin || !["http:", "https:"].includes(parsedOrigin.protocol) ||
    parsedOrigin.username || parsedOrigin.password || parsedOrigin.pathname !== "/" ||
    parsedOrigin.search || parsedOrigin.hash || parsedOrigin.origin !== API_ORIGIN) {
  throw new Error("XIANGWAN_ADMIN_API_ORIGIN must be one absolute origin");
}

const nextConfig = {
  reactStrictMode: true,
  output: "standalone",
  env: {
    // Brand-banner uploads return a relative media path; the editor turns it
    // into the absolute public URL the brand profile requires.
    NEXT_PUBLIC_API_ORIGIN: API_ORIGIN,
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "Cache-Control", value: "no-store" },
          {
            key: "Content-Security-Policy",
            value: `default-src 'self'; script-src 'self' 'unsafe-inline'${developmentScriptSource}; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; connect-src 'self'; media-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'`,
          },
          { key: "Permissions-Policy", value: "camera=(self), microphone=(), geolocation=()" },
          { key: "Referrer-Policy", value: "no-referrer" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Strict-Transport-Security", value: "max-age=31536000; includeSubDomains" },
        ],
      },
    ];
  },
  async rewrites() {
    return [
      {
        source: "/api/v1/xiangwan/admin/:path*",
        destination: `${API_ORIGIN}/api/v1/xiangwan/admin/:path*`,
      },
      {
        // The custom-activity editor reads the published policy versions from
        // the public Xiangwan API. Keep this request same-origin so the editor
        // works when the API is on a different origin in development.
        source: "/api/v1/xiangwan/public-policies",
        destination: `${API_ORIGIN}/api/v1/xiangwan/public-policies`,
      },
      {
        // 公开媒体(covers)与 admin API 一样走同源代理:开发环境管理台是
        // https://前端 origin,后端是 http origin,浏览器直连媒体地址会触发
        // mixed-content 并被 CSP img-src 拦截;预览统一走同源 rewrite。
        source: "/api/v1/xiangwan/covers/:path*",
        destination: `${API_ORIGIN}/api/v1/xiangwan/covers/:path*`,
      },
      {
        // Brand hero previews use the same HTTPS same-origin path in local
        // development; production may still persist the API-origin URL.
        source: "/api/v1/xiangwan/brand-hero/:path*",
        destination: `${API_ORIGIN}/api/v1/xiangwan/brand-hero/:path*`,
      },
    ];
  },
};

export default nextConfig;
