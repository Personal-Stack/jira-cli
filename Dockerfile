# syntax=docker/dockerfile:1
# Multi-stage build: static binary on a distroless, non-root runtime image.
# No credentials are ever copied into the image; mount them at runtime.

# The build stage runs on the builder's native platform and cross-compiles,
# so multi-arch images (linux/amd64, linux/arm64) build without emulation.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
# Verify generated code is in sync before building (reproducible builds).
RUN go run ./cmd/jiragen check
RUN mkdir -p /out && scripts/third-party-licenses.sh /out/THIRD_PARTY_LICENSES && cp LICENSE NOTICE LEGAL.md /out/
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/jiracli ./cmd/jiracli

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/Personal-Stack/jira-cli" \
      org.opencontainers.image.title="Personal Stack jira-cli" \
      org.opencontainers.image.description="Unofficial command-line client for Jira Data Center (not affiliated with Atlassian)" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/jiracli /usr/local/bin/jiracli
COPY --from=build /out/LICENSE /out/NOTICE /out/LEGAL.md /out/THIRD_PARTY_LICENSES /usr/share/doc/jira-cli/
# No OS keyring in containers: credentials come from mounted files/env.
ENV JIRA_NO_KEYRING=1 \
    JIRA_CONFIG=/config/config.yaml
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/jiracli"]
CMD ["capabilities"]
