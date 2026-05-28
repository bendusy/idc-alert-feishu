FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o idc-alert-feishu .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=build /app/idc-alert-feishu .
EXPOSE 8000
ENTRYPOINT ["./idc-alert-feishu"]
