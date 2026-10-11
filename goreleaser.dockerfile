FROM gcr.io/distroless/static-debian12:nonroot

COPY external-dns-selectel-private-webhook /external-dns-selectel-private-webhook
ENTRYPOINT ["/external-dns-selectel-private-webhook"]
