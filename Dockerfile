# ---- 前端 ----
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
RUN npm run build && node -e "require('fs').writeFileSync('dist/.gitkeep','')"

# ---- Go 二进制 ----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 先放前端产物再编译：go:embed 会把它打进二进制
COPY --from=web /web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/vaultd ./cmd/vaultd

# ---- 运行 ----
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/vaultd /vaultd
WORKDIR /app
EXPOSE 8788
ENTRYPOINT ["/vaultd"]
CMD ["-addr", "0.0.0.0:8788", "-db", "data/app.db"]
