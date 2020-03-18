FROM golang:1.14-alpine as builder
WORKDIR /go/main
COPY . .
ENV GO111MODULE=on
RUN go generate
RUN go build -o main -v

FROM alpine:latest
RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*
RUN apk add git docker-cli
COPY --from=builder /go/main/main /main
COPY --from=builder /go/main/template/ /template/
EXPOSE 8090
ENTRYPOINT ["/main"]
# any flags here, for example use the data folder
CMD ["--debug"] 