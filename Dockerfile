FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/settler ./cmd/settler \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/verify ./cmd/verify

FROM alpine:3.20 AS api
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/api /api
EXPOSE 8080
USER nobody
ENTRYPOINT ["/api"]

FROM alpine:3.20 AS settler
RUN apk add --no-cache ca-certificates
COPY --from=build /out/settler /settler
USER nobody
ENTRYPOINT ["/settler"]

FROM alpine:3.20 AS tools
RUN apk add --no-cache ca-certificates
COPY --from=build /out/migrate /migrate
COPY --from=build /out/verify /verify
ENTRYPOINT ["/migrate"]
