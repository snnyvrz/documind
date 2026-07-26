FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -o /documind-api .

FROM alpine:3.23

RUN adduser -D -H -u 10001 documind && apk add --no-cache su-exec

COPY --from=build /documind-api /usr/local/bin/documind-api
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint
RUN chmod +x /usr/local/bin/docker-entrypoint

EXPOSE 1323

ENTRYPOINT ["/usr/local/bin/docker-entrypoint"]