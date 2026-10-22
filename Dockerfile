# straum-console image (CON-D-6 A). Built by .github/workflows/image.yml from
# a published release's linux archives, so it carries the release's own,
# attested binary; `make image` builds one from source for a local try.
#
# The console listens on loopback unless it signs people in (CON-D-8), so run
# it with the host's network:
#
#   docker run --network host straumio/straum-console --node 0=127.0.0.1:9100,…
#
# A published port (-p) reaches it only with TLS, a password file and
# --public-host, listening on :8443: inside its own network namespace the
# container's loopback is not the host's.

# Static, no shell, no package manager; runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETARCH
COPY image/linux-${TARGETARCH}/straum-console /usr/local/bin/straum-console
USER nonroot
ENTRYPOINT ["/usr/local/bin/straum-console"]
