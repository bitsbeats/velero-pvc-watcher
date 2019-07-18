FROM alpine

ADD ./controller /controller

ENTRYPOINT /controller
