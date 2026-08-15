ARG GO_VERSION=1.26.4
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

# compose.yaml can override these; otherwise they are read from the Git
# checkout copied into the build stage below.
ARG VERSION
ARG COMMIT
ARG DATE

RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}" && \
    commit="${COMMIT:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}" && \
    date="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${date}" \
      -o /out/vitald ./cmd/vitald

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/vitald /usr/local/bin/vitald
RUN addgroup -S vitald && adduser -S -G vitald vitald
USER vitald
ENTRYPOINT ["vitald"]
CMD ["status"]
