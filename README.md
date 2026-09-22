# check-mk-passive-agent

Receiver for Checkmk agents that push their output (`check-mk-agent-push`)
instead of being polled. Each push is written to
`/var/lib/monitoring/passive/<hostname>`; the Checkmk site reads it back with
`grab-check` (agent data) and `host-grab-check` (host state from freshness).

```
agent (cron, every minute)
  -> HTTPS POST /push-agent/passive/<hostname>
  -> nginx (TLS termination, optional basic auth, proxy_pass)
  -> check-mk-passive-agent (127.0.0.1:8611)
  -> /var/lib/monitoring/passive/<hostname>
  -> Checkmk site: grab-check / host-grab-check
```

The service is a single static Go binary with no dependencies. It replaces the
earlier PHP receiver (`passive.php` behind Apache) and speaks the same
protocol, so agents need no changes.

## Protocol

`POST /push-agent/passive` or `POST /push-agent/passive/<anything>`,
`multipart/form-data`. Agents also send HTTP basic auth; the service ignores
it and leaves that check to the reverse proxy (see nginx below).

| Field      | Content                                        |
|------------|------------------------------------------------|
| `token`    | shared token, must match one from the config   |
| `hostname` | host name, used as the file name               |
| `md5`      | md5 (hex) of the uncompressed agent output     |
| `payload`  | agent output, gzip compressed (file upload)    |

Checks run in this order; the status codes are the ones the PHP receiver used:

| Condition                                  | Response            |
|--------------------------------------------|---------------------|
| path outside `/push-agent/passive`         | 404                 |
| method other than POST                     | 405                 |
| request body over `CMK_PASSIVE_MAX_BODY`   | 413                 |
| missing `token`                            | 406                 |
| missing `hostname` or `md5`                | 405                 |
| invalid `hostname`                         | 403 `FAIL:HOSTNAME` |
| unknown `token`                            | 404                 |
| missing `payload`                          | 400                 |
| bad gzip, md5 mismatch, too large, I/O     | 403 `FAIL:WRITE`    |
| stored                                     | 200                 |

`hostname` must be a valid DNS host name (labels of letters, digits and
hyphens, max 253 characters), so it can never escape the storage directory.
Output is written to a temporary file in the storage directory and renamed
over `<hostname>`, so Checkmk never reads a partial file. Tokens are compared
in constant time. Rejected requests are logged to stdout
(journald); successful pushes are not logged.

Once an hour the service removes spool files older than
`CMK_PASSIVE_RETENTION`, so hosts that were decommissioned do not keep
answering with stale data, and leftover temporary files older than an hour.

## Configuration

Runtime settings, `/etc/default/check-mk-passive-agent`:

| Variable                  | Default                             |
|---------------------------|-------------------------------------|
| `CMK_PASSIVE_LISTEN`      | `127.0.0.1:8611`                    |
| `CMK_PASSIVE_STORAGE`     | `/var/lib/monitoring/passive`       |
| `CMK_PASSIVE_MAX_BODY`    | `8388608` (8 MiB, request body)     |
| `CMK_PASSIVE_MAX_PAYLOAD` | `33554432` (32 MiB, after gunzip)   |
| `CMK_PASSIVE_RETENTION`    | `168h` (7 days, `0` disables cleanup) |
| `CMK_PASSIVE_CONFIG`      | see below                           |

Tokens, `/etc/site/monitoring/agent/config.json` (`root:root`, `0600`):

```json
{
  "tokens": ["per-host-secret"]
}
```

Each entry is a secret, not a bearer token. A push is accepted when its
`token` equals the value derived for the host it claims to be:

```
token = sha256(secret + ":" + hostname)     # hex
```

A host therefore only ever holds the token minted for its own name: the secret
stays on this host and on the provisioning system, so a compromised host
cannot compute the token of another host. The field is a list so a secret can
be rotated without downtime - both the old and the new one are accepted while
the fleet is updated. At least one entry is required.

The unit passes this file to the service with `LoadCredential=`, so the
service user never needs read access to `/etc/site/monitoring/agent`. The service
reads `$CREDENTIALS_DIRECTORY/config`; outside systemd it falls back to
`/etc/site/monitoring/agent/config.json`. `CMK_PASSIVE_CONFIG` overrides both. The
service refuses to start without at least one token. Changes
need `systemctl restart check-mk-passive-agent`.

## systemd

`dist/check-mk-passive-agent.service` runs the service as `check-mk-passive-agent`, a
system user the package creates. systemd creates the storage directory
(`StateDirectory=monitoring/passive`, mode `0750`) owned by the service user
and group; files are written `0640`.

The Checkmk site must be able to read the directory, so the service group has
to be the site group. Set it with a drop-in, e.g. for a site named `site`:

```
# /etc/systemd/system/check-mk-passive-agent.service.d/group.conf
[Service]
Group=site
```

```
systemctl daemon-reload
systemctl enable --now check-mk-passive-agent
```

On a host that already has `/var/lib/monitoring/passive` from the PHP
receiver, systemd takes the directory over on first start. The old ACL entry
for `www-data` is harmless and can be removed once Apache no longer serves the
endpoint.

## nginx

nginx terminates TLS, checks HTTP basic auth and proxies only the push
endpoint to the service. Agents carry the credentials in their push URL
(`https://user:password@host/push-agent/passive`). Basic auth is a perimeter
filter kept outside the service: dropping `auth_basic` from the vhost disables
it without touching the service. The user file holds crypt(3) hashes, so the
password is not readable on the monitoring host:

```
passive:$6$rounds=656000$...
```

`dist/nginx-agent.conf.example` is a minimal vhost; replace the static
certificate lines with the ACME module configuration when using nginx from
nginx.org. Keep `client_max_body_size` at or above `CMK_PASSIVE_MAX_BODY`.

## Checkmk site

The helper scripts live in `helpers/`. Install them as the site user, into the
site's own `local/bin`:

```
make install
```

Host check command, for hosts with the label `HOST:PASSIVE`:

```
host-grab-check "$HOSTNAME$" "$HOSTADDRESS$" "$_HOSTTAGS$"
```

![Passive Host Setup](.github/host-passive.png)

Datasource program, for hosts with the label `DS:PASSIVE`:

```
grab-check "$HOSTNAME$" "$HOSTADDRESS$" "$_HOSTTAGS$"
```

![Passive Check Setup](.github/check-passive.png)

## Build

Go sources live in `src/`.

```
make test
make build        # static linux/amd64 binary: ./check-mk-passive-agent
```

GitHub Actions (`.github/workflows/build.yml`) runs the tests and builds the
linux/amd64 binary on every push; a `v*` tag attaches the binary and its
sha256 to a GitHub release. The Debian package is built separately.
