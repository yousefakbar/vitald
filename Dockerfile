ARG GO_VERSION=1.26.4
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/vitald ./cmd/vitald

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/vitald /usr/local/bin/vitald
RUN addgroup -S vitald && adduser -S -G vitald vitald
USER vitald
ENTRYPOINT ["vitald"]
CMD ["status"]
