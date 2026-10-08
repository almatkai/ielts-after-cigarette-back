# The verified runtime already includes FFmpeg, CA roots and the non-root user.
FROM ghcr.io/almatkai/ielts-after-cigarette-back:main
COPY --chown=65532:65532 bin/api /app/api
COPY --chown=65532:65532 bin/worker /app/worker
USER 65532:65532
ENTRYPOINT ["/app/api"]
