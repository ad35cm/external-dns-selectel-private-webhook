# ExternalDNS webhook for Selectel Private DNS

An [ExternalDNS webhook provider](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/webhook-provider.md)
for [Selectel Private DNS](https://docs.selectel.ru/en/cloud-servers/private-dns/), the DNS service for zones that resolve
only inside Selectel private networks. It manages the record sets of private zones in one pool through the
[Private DNS API](https://docs.selectel.ru/en/api/private-dns/).

This project is a fork of [selectel/external-dns-selectel-webhook](https://github.com/selectel/external-dns-selectel-webhook),
which supports only the public DNS hosting API. The webhook server, Keystone authentication, flag and environment
configuration, domain filter and metrics are kept; the DNS backend is replaced. See [NOTICE](NOTICE) for attribution.

## How it works

- **Zones.** The webhook lists the private zones of the project in the configured pool. Zones that do not match
  `--domain-filter` and zones reserved by another Selectel service (for example Managed Databases) are not managed.
- **Records.** A, AAAA, CNAME, MX and TXT are supported. One ExternalDNS endpoint maps to one record set (name and type);
  its targets are the record set values. An endpoint without a TTL creates a record set that inherits the zone TTL.
- **Changes.** Every sync sends one hard update (`PUT /zones/{zone_id}/recordset`) per changed zone. The API applies it
  atomically, deletions first, so a record can change type (for example CNAME to A) in one sync.
- **Auto records.** When a private network is added to a zone, Private DNS creates A/AAAA records for its ports and marks
  them `generated` (Auto in the control panel). The webhook reports them to ExternalDNS, which treats them as records it
  does not own, and never modifies or deletes them: any change to an Auto record set, or a CNAME at its name, is dropped
  with a `skipping change to an Auto record set` warning.
- **Limits** enforced by the API: 1,000 record sets per zone, 100 values per record set, one value per CNAME, and the
  exchange host of an MX record must be inside the zone.
- **Ports.** The webhook API listens on `127.0.0.1:8888`, reachable only from the ExternalDNS container of the same pod.
  `/healthz` and `/metrics` listen on `:8080`, the `http-webhook` port that the ExternalDNS Helm chart probes.

## Prerequisites

1. A private zone in the pool, for example `prod-ru-6.burnsdev.com.` in `ru-6`.
2. A [service user](https://docs.selectel.ru/en/access-control/user-types/#service-users) with the
   `vpc.private_network.admin` role (or `member`) in the project that owns the zone. The webhook authenticates with a
   project-scoped Keystone token issued for this user.
3. The account ID (the `OS_PROJECT_DOMAIN_NAME` value of the project's `rc.sh`) and the project ID.

## Kubernetes deployment

The image is published as `ghcr.io/ad35cm/external-dns-selectel-private-webhook:<version>` for `linux/amd64` and
`linux/arm64`. Run it as the `provider.webhook` sidecar of the
[external-dns Helm chart](https://github.com/kubernetes-sigs/external-dns/tree/master/charts/external-dns) (tested with
chart 1.23.0, ExternalDNS v0.23.0).

Create the Secret with the service-user credentials:

```shell
kubectl create namespace external-dns
kubectl create secret generic external-dns-selectel-private \
  --namespace external-dns \
  --from-literal=account-id='<account id>' \
  --from-literal=project-id='<project id>' \
  --from-literal=username='<service user name>' \
  --from-literal=password='<service user password>'
```

| Secret key   | Environment variable | Content                    |
|--------------|----------------------|----------------------------|
| `account-id` | `ACCOUNT_ID`         | Selectel account ID        |
| `project-id` | `PROJECT_ID`         | ID of the zone's project   |
| `username`   | `USERNAME`           | Service user name          |
| `password`   | `PASSWORD`           | Service user password      |

Helm values:

```yaml
provider:
  name: webhook
  webhook:
    image:
      repository: ghcr.io/ad35cm/external-dns-selectel-private-webhook
      tag: 1.0.0
    args:
      - --pool=ru-6
      - --domain-filter=prod-ru-6.burnsdev.com
    env:
      - name: ACCOUNT_ID
        valueFrom:
          secretKeyRef:
            name: external-dns-selectel-private
            key: account-id
      - name: PROJECT_ID
        valueFrom:
          secretKeyRef:
            name: external-dns-selectel-private
            key: project-id
      - name: USERNAME
        valueFrom:
          secretKeyRef:
            name: external-dns-selectel-private
            key: username
      - name: PASSWORD
        valueFrom:
          secretKeyRef:
            name: external-dns-selectel-private
            key: password
    securityContext:
      runAsNonRoot: true
      readOnlyRootFilesystem: true
      allowPrivilegeEscalation: false
      capabilities:
        drop: ["ALL"]

domainFilters:
  - prod-ru-6.burnsdev.com
policy: sync
registry: txt
txtOwnerId: prod-ru-6-selectel
sources:
  - service
  - ingress
```

The chart's default probes (`/healthz` on the `http-webhook` port) and webhook service port (8080) work unchanged.
Set `provider.webhook.serviceMonitor` to scrape the webhook's `/metrics`.

## Configuration

Every flag can also be set through the environment variable in the second column. Command-line flags take precedence.

| Flag              | Environment variable | Default                                    | Description |
|-------------------|----------------------|--------------------------------------------|-------------|
| `--pool`          | `POOL`               |                                            | Selectel pool of the zones, e.g. `ru-6`. The API endpoint is `https://<pool>.cloud.api.selcloud.ru/private-dns/v1`. Required unless `--base-url` is set. |
| `--base-url`      | `BASE_URL`           |                                            | Private DNS API endpoint; overrides `--pool`. |
| `--project-id`    | `PROJECT_ID`         |                                            | Required. Project of the zones; the Keystone token is scoped to it. |
| `--account-id`    | `ACCOUNT_ID`         |                                            | Required. Selectel account ID (Keystone domain). |
| `--username`      | `USERNAME`           |                                            | Required. Service user name. |
| `--password`      | `PASSWORD`           |                                            | Required. Service user password. |
| `--auth-url`      | `AUTH_URL`           | `https://cloud.api.selcloud.ru/identity/v3` | Keystone endpoint. |
| `--domain-filter` | `DOMAIN_FILTER`      | all zones                                  | Zones to manage. Repeat the flag or separate domains with commas. ExternalDNS also receives this filter. |
| `--api-port`      | `API_PORT`           | `8888`                                     | Port of the webhook API on `127.0.0.1`. |
| `--health-port`   | `HEALTH_PORT`        | `8080`                                     | Port of `/healthz` and `/metrics` on all interfaces. |
| `--worker`        | `WORKER`             | `10`                                       | Zones processed concurrently. Keep it low to avoid 429 responses from the API. |
| `--dry-run`       | `DRY_RUN`            | `false`                                    | Log the record set changes instead of applying them. |
| `--log-level`     | `LOG_LEVEL`          | `info`                                     | `debug`, `info`, `warn` or `error`. |

The webhook exits at startup if `--pool`/`--base-url` or any credential is missing.

## Development

```shell
make build   # bin/external-dns-selectel-private-webhook
make test    # unit tests against an in-memory Private DNS API
make lint    # golangci-lint v2
```

Run against a real pool, with the credentials in the environment:

```shell
export ACCOUNT_ID=... PROJECT_ID=... USERNAME=... PASSWORD=...
./bin/external-dns-selectel-private-webhook --pool=ru-6 --domain-filter=example.internal --log-level=debug
curl -s localhost:8888/records
```

## Release

Pushing a `v*` tag runs [the workflow](.github/workflows/go.yml): lint, build and test, then GoReleaser publishes the
GitHub release and the images `ghcr.io/ad35cm/external-dns-selectel-private-webhook:<version>` (multi-arch manifest),
`:<version>-amd64` and `:<version>-arm64`. `make release-check` builds the same artifacts locally without publishing.
