FROM node:22-alpine AS ui-builder
WORKDIR /ui
COPY package.json tsconfig.json vite.config.ts index.html ./
COPY src ./src
RUN npm install && npm run build

FROM golang:1.25-alpine AS go-builder
WORKDIR /src
COPY go.mod main.go ./
COPY --from=ui-builder /ui/dist ./dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/openksm-web .

FROM alpine:3.21@sha256:bc21487101ae47172509130ee1092cc273a831968d64b617776d1f69c81e42ab
RUN adduser -D -u 10001 openksm
WORKDIR /app
COPY --from=go-builder /out/openksm-web /app/openksm-web
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/openksm-web"]
