# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM docker.io/library/golang@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS source

ENV GOPROXY=https://goproxy.cn|https://proxy.golang.org|direct

ARG WUKONG_COMMIT=a888f89533d0e7d1b2030e06504ca97f1ad891d4
ARG WUKONG_VERSION=v2.2.5-20260422-linli.3
ARG WUKONG_ARCHIVE_SHA256=a6c225123ea99130f38c4ef828f85ff6648ccfda4e9a9acba50354ddb2965306
ARG WUKONG_PATCH_SHA256=6163ffc38a5bf4fbed2d7c94610c336708066f05df5a2bb5f49e20951f16b01a

RUN apk add --no-cache patch=2.8-r0
ADD --checksum=sha256:a6c225123ea99130f38c4ef828f85ff6648ccfda4e9a9acba50354ddb2965306 \
    https://codeload.github.com/WuKongIM/WuKongIM/tar.gz/a888f89533d0e7d1b2030e06504ca97f1ad891d4 /tmp/wukongim.tar.gz
COPY infra/wukongim/server-patch/wukongim-v2.2.5-linli.patch /tmp/linli.patch
RUN echo "${WUKONG_ARCHIVE_SHA256}  /tmp/wukongim.tar.gz" | sha256sum -c - && \
    echo "${WUKONG_PATCH_SHA256}  /tmp/linli.patch" | sha256sum -c - && \
    mkdir -p /src && \
    tar -xzf /tmp/wukongim.tar.gz --strip-components=1 -C /src && \
    cd /src && patch --batch --forward -p1 < /tmp/linli.patch

WORKDIR /src
RUN for attempt in 1 2 3; do \
      go mod download && exit 0; \
      echo "go mod download attempt ${attempt} failed; retrying" >&2; \
      sleep 2; \
    done; \
    exit 1

FROM source AS verify
RUN go test ./pkg/wkcache ./internal/plugin ./internal/api ./internal/eventbus ./internal/user/handler
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-w -s -X main.Commit=${WUKONG_COMMIT} -X main.CommitDate=2026-04-22 -X main.Version=${WUKONG_VERSION} -X main.TreeState=patched" \
    -o /out/wukongim ./main.go

FROM docker.io/library/alpine@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659 AS runtime
ENV TZ=Asia/Shanghai
LABEL org.opencontainers.image.title="Linli WuKongIM" \
      org.opencontainers.image.version="v2.2.5-20260422-linli.3" \
      org.opencontainers.image.revision="a888f89533d0e7d1b2030e06504ca97f1ad891d4" \
      org.opencontainers.image.source="https://github.com/WuKongIM/WuKongIM" \
      org.opencontainers.image.licenses="Apache-2.0" \
      com.linli.wukongim.patch-sha256="6163ffc38a5bf4fbed2d7c94610c336708066f05df5a2bb5f49e20951f16b01a"
RUN apk add --no-cache ca-certificates=20260611-r0 tzdata=2026c-r0
COPY --from=verify /out/wukongim /home/app
COPY --from=verify /src/config/wk.yaml /root/wukongim/wk.yaml
WORKDIR /home
ENTRYPOINT ["/home/app", "--config=/root/wukongim/wk.yaml", "--ignoreMissingConfig=true"]
