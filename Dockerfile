FROM golang:1.18-stretch

COPY . /build

WORKDIR /build

RUN go get -t .
RUN go build


FROM phusion/baseimage:18.04-1.0.0

COPY --from=0 /build/knoxcache /knox

CMD /knox
