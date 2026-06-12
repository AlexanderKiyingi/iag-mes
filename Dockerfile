# syntax=docker/dockerfile:1.7
#
# Monorepo:   docker build -f services/operations/mes/Dockerfile --target monorepo .
# Standalone: docker build --target standalone .

FROM golang:1.25-alpine AS base
RUN apk add --no-cache git ca-certificates
ENV PLATFORM_GO_DEP=/deps/platform-go

FROM base AS platform-go-clone
ARG IAG_META_REF=main
ARG IAG_META_REPO=https://github.com/AlexanderKiyingi/IAG_multi_backend.git
# The meta-repo is private, so an anonymous clone fails in CI with
# "could not read Username for 'https://github.com'". Railway's Metal builder
# does not support BuildKit secret mounts, so pass a GitHub token as a build
# ARG (set GH_TOKEN as a build variable on the service). It is injected into the
# clone URL only at build time and is not retained in the final image — the
# standalone image copies platform-go out via --from, never the token or .git.
# When GH_TOKEN is empty the clone falls back to the anonymous URL.
ARG GH_TOKEN=
RUN set -e; \
    CLONE_URL="${IAG_META_REPO}"; \
    if [ -n "${GH_TOKEN}" ]; then \
      CLONE_URL=$(printf '%s' "${IAG_META_REPO}" | sed "s#https://#https://x-access-token:${GH_TOKEN}@#"); \
    fi; \
    git clone --depth 1 --branch "${IAG_META_REF}" "${CLONE_URL}" /tmp/iag \
    && mv /tmp/iag/shared/platform-go "${PLATFORM_GO_DEP}" \
    && rm -rf /tmp/iag

FROM base AS platform-go-copy
COPY shared/platform-go ${PLATFORM_GO_DEP}

FROM base AS build-standalone
COPY --from=platform-go-clone ${PLATFORM_GO_DEP} ${PLATFORM_GO_DEP}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod edit -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /mes ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /mes-jobs ./cmd/mes-jobs

FROM base AS build-monorepo
COPY --from=platform-go-copy ${PLATFORM_GO_DEP} ${PLATFORM_GO_DEP}
WORKDIR /src/services/operations/mes
COPY services/operations/mes/go.mod services/operations/mes/go.sum ./
RUN go mod edit -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY services/operations/mes/ .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /mes ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /mes-jobs ./cmd/mes-jobs

FROM alpine:3.21 AS monorepo
RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app
COPY --from=build-monorepo /mes /app/mes
COPY --from=build-monorepo /mes-jobs /app/mes-jobs
ENV PORT=4003 \
    GIN_MODE=release \
    AUTO_MIGRATE=false
EXPOSE 4003
HEALTHCHECK --interval=15s --timeout=5s --start-period=25s --retries=5 \
  CMD wget -q -O /dev/null http://127.0.0.1:4003/ready || exit 1
USER nobody
ENTRYPOINT ["/app/mes"]

FROM alpine:3.21 AS standalone
RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app
COPY --from=build-standalone /mes /app/mes
COPY --from=build-standalone /mes-jobs /app/mes-jobs
ENV PORT=4003 \
    GIN_MODE=release \
    AUTO_MIGRATE=false
EXPOSE 4003
HEALTHCHECK --interval=15s --timeout=5s --start-period=25s --retries=5 \
  CMD wget -q -O /dev/null http://127.0.0.1:4003/ready || exit 1
USER nobody
ENTRYPOINT ["/app/mes"]
