FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod main.go ./
COPY public ./public
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/openksm-web .

FROM alpine:3.21@sha256:bc21487101ae47172509130ee1092cc273a831968d64b617776d1f69c81e42ab
RUN adduser -D -u 10001 openksm
WORKDIR /app
COPY --from=builder /out/openksm-web /app/openksm-web
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/openksm-web"]
