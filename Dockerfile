FROM	golang:1.20.2 as build
WORKDIR	/conntrack-stats-exporter
COPY	go.mod go.sum ./
RUN	go mod download
COPY	. .
RUN	go mod verify
RUN	./build.sh

FROM	alpine:3.17.2
COPY	--from=build /conntrack-stats-exporter/conntrack-stats-exporter /usr/local/sbin/
RUN	apk update && \
	apk --no-cache upgrade && \
	apk --no-cache add conntrack-tools
ENTRYPOINT	[ "/usr/local/sbin/conntrack-stats-exporter" ]
