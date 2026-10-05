# 享玩运营后台(Next.js standalone)。构建上下文必须是仓库根目录。
# XIANGWAN_ADMIN_API_ORIGIN 在构建期就必须存在:next.config.mjs 会校验它,
# 并把同源 /api/v1/xiangwan/admin/* 的 rewrite 烘焙进产物;运行期只需 PORT。
FROM node:24-alpine AS build

WORKDIR /src

RUN npm install --global pnpm@10.8.0

ARG XIANGWAN_ADMIN_API_ORIGIN
ENV XIANGWAN_ADMIN_API_ORIGIN=$XIANGWAN_ADMIN_API_ORIGIN

RUN test -n "$XIANGWAN_ADMIN_API_ORIGIN"

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/xiangwan-admin-web ./apps/xiangwan-admin-web
RUN pnpm install --filter xiangwan-admin-web --frozen-lockfile --ignore-scripts \
  && pnpm --filter xiangwan-admin-web build

FROM node:24-alpine

LABEL org.opencontainers.image.source="https://github.com/wzyhn/xiangwanai"

WORKDIR /app
ENV NODE_ENV=production \
    HOSTNAME=0.0.0.0 \
    PORT=3000

RUN addgroup -S xiangwan && adduser -S -G xiangwan xiangwan

COPY --from=build --chown=xiangwan:xiangwan /src/apps/xiangwan-admin-web/.next/standalone ./
COPY --from=build --chown=xiangwan:xiangwan /src/apps/xiangwan-admin-web/.next/static ./apps/xiangwan-admin-web/.next/static

USER xiangwan
EXPOSE 3000

CMD ["node", "apps/xiangwan-admin-web/server.js"]
