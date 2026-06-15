FROM golang:1.22-alpine AS build
WORKDIR /app
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG BUILT_BY=docker
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/bendusy/idc-alert-feishu/cmd.version=${VERSION} -X github.com/bendusy/idc-alert-feishu/cmd.commit=${COMMIT} -X github.com/bendusy/idc-alert-feishu/cmd.date=${DATE} -X github.com/bendusy/idc-alert-feishu/cmd.builtBy=${BUILT_BY}" \
    -o idc-alert-feishu .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=build /app/idc-alert-feishu .
EXPOSE 8000
ENTRYPOINT ["./idc-alert-feishu"]
