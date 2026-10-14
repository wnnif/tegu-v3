FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /tegu .
FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 tegu && mkdir -p /var/lib/tegu /backups && chown -R tegu:tegu /var/lib/tegu /backups
COPY --from=build /tegu /usr/local/bin/tegu
USER tegu
EXPOSE 8080
ENTRYPOINT ["tegu"]
