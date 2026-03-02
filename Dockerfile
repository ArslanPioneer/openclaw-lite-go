# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /src

ARG APP_VERSION=dev
ARG APP_COMMIT=unknown

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags="-s -w -X openclaw-lite-go/internal/runtime.AppVersion=${APP_VERSION} -X openclaw-lite-go/internal/runtime.AppCommit=${APP_COMMIT}" \
  -o /out/clawlite ./cmd/clawlite

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app
RUN mkdir -p /app/data
COPY --from=build /out/clawlite /app/clawlite

EXPOSE 18080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:18080/healthz || exit 1

ENTRYPOINT ["/app/clawlite"]
CMD ["run", "--config", "/app/data/config.json"]
