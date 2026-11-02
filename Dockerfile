# syntax=docker/dockerfile:1
# Multi-stage build: static binary on a distroless, non-root runtime image.
# No credentials are ever copied into the image; mount them at runtime.

FROM golang:1.27 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
# Verify generated code is in sync before building (reproducible builds).
RUN go run ./cmd/jiragen check
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/jira ./cmd/jira

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/jira /usr/local/bin/jira
# No OS keyring in containers: credentials come from mounted files/env.
ENV JIRA_NO_KEYRING=1 \
    JIRA_CONFIG=/config/config.yaml
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/jira"]
CMD ["capabilities"]
