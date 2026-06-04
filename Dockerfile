# syntax=docker/dockerfile:1.7
# Monorepo: docker build -f services/operations/mes/Dockerfile --target monorepo .

FROM golang:1.23-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src/services/operations/mes
COPY services/operations/mes/go.mod services/operations/mes/go.sum ./
RUN go mod download
COPY services/operations/mes/ .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /mes ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /mes /app/mes
ENV PORT=4003
EXPOSE 4003
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=5 \
  CMD wget -q -O /dev/null http://127.0.0.1:4003/health || exit 1
USER nobody
ENTRYPOINT ["/app/mes"]
