# The server shells out to git, so the image needs git plus the ssh client
# for ssh:// upstreams. ca-certificates covers https upstreams.
FROM alpine:3.20
ARG TARGETPLATFORM
RUN apk add --no-cache ca-certificates git openssh-client \
    && adduser -D -h /data -u 1000 git

# GoReleaser lays the build context out as <platform>/<binary>.
COPY $TARGETPLATFORM/git-mirror /usr/bin/git-mirror

ENV GIT_MIRROR_DIR=/data
VOLUME /data
# Defaults of a new config.json: 8080 git over HTTP(S) (token auth), 2222 git over SSH (key auth)
EXPOSE 8080 2222
USER git
WORKDIR /data
ENTRYPOINT ["/usr/bin/git-mirror"]
CMD ["server"]
