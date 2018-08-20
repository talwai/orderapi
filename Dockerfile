FROM golang:alpine

RUN mkdir -p /go/src/github.com/talwai/orderapi
ADD . /go/src/github.com/talwai/orderapi
RUN wget -O - https://raw.githubusercontent.com/golang/dep/master/install.sh | sh

WORKDIR /go/src/github.com/talwai/orderapi 
RUN dep ensure
RUN go build -o main . 

CMD ["/go/src/github.com/talwai/orderapi/main"]
