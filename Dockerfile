FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY doomsday /usr/local/bin/doomsday
ENTRYPOINT ["doomsday"]
