FROM golang:1.25.13-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/xiangwan ./cmd/xiangwan \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sqlmigrate ./cmd/sqlmigrate

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S xiangwan && adduser -S -G xiangwan xiangwan \
 && mkdir -p /data/xiangwan && chown xiangwan:xiangwan /data/xiangwan
WORKDIR /app
LABEL org.opencontainers.image.source="https://github.com/wzyhn/xiangwanai"
ENV TZ=Asia/Shanghai
USER xiangwan

FROM runtime AS xiangwan
COPY --from=build /out/xiangwan /app/xiangwan
EXPOSE 8080
ENTRYPOINT ["/app/xiangwan"]

FROM runtime AS xiangwan-migrate
COPY --from=build /out/sqlmigrate /app/sqlmigrate
COPY deploy/sql /app/deploy/sql
ENTRYPOINT ["/app/sqlmigrate"]
