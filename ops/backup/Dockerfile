FROM postgres:17-alpine
RUN apk add --no-cache bash aws-cli coreutils findutils
COPY backup.sh /usr/local/bin/iac-backup
ENTRYPOINT ["bash", "/usr/local/bin/iac-backup"]
