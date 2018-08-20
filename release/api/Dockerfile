FROM alpine
RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*

RUN mkdir /app
WORKDIR /app
ADD ./app_amd64 .

EXPOSE 8080
CMD ["/app/app_amd64"]
