# Unprivileged execution (`BL_SANDBOX_USER`)

The sandbox API is the image entrypoint, and it needs privileges: agent drive
mounts are FUSE mounts (`CAP_SYS_ADMIN`), the egress tunnel is WireGuard
(`CAP_NET_ADMIN`), the MITM CA is merged into the system trust store, keep-alive
toggles scale-to-zero, and port probing inspects other processes.

A Docker `USER` directive cannot express that split: the runtime applies it to
PID 1, which de-privileges the API itself — that is why images built with
`bl deploy --experimental` lose drive mounting.

`BL_SANDBOX_USER` expresses it instead. The API stays privileged; everything it
does *on behalf of the user* is dropped to an unprivileged identity.

## Enabling it

```dockerfile
RUN adduser -D -u 10001 -h /blaxel app

ENV BL_SANDBOX_USER=app       # also accepts "10001", "app:app", "10001:10001"
```

Equivalently, `sandbox-api --user app` — the flag wins over the environment.
Use an entrypoint when the image also needs root-only preparation before the
workload identity applies:

```sh
#!/bin/sh
set -eu
SANDBOX_USER="${BL_SANDBOX_USER:-app}"
chown -R "$SANDBOX_USER" "${HOME:-/blaxel}"
exec /usr/local/bin/sandbox-api --user "$SANDBOX_USER" "$@"
```

Do **not** add a `USER` directive: that is the mechanism this replaces.
`hub/nonroot/` is a working example of both halves.

If the value cannot be resolved, or resolves to uid 0, the API refuses to start.
Failing open would hand every workload the privileges the feature exists to
remove.

## What runs as the workload user

| Surface | Mechanism |
|---|---|
| `POST /process`, `/process/{id}/exec`, restarts, MCP `processExecute`, codegen | `SysProcAttr.Credential` (uid, gid, supplementary groups) |
| `/ws/terminal` | same credential, plus the PTY slave is chowned to the user before the shell starts |
| `-c/--command` startup command | same credential |
| every `/filesystem` operation, including multipart completion | `setfsuid(2)`/`setfsgid(2)` around the operation |

`HOME`, `USER` and `LOGNAME` are rewritten to match the identity in all spawned
environments.

## What stays privileged

Drive mounts, WireGuard, CA bundle, keep-alive, port/network inspection,
process supervision and log files. So that a mounted drive is still usable:

- `-map.uid` / `-map.gid` default to the workload uid/gid (drive content is
  owned by filer uid 0), overridable per request or with
  `BLFS_UID_MAP`/`BLFS_GID_MAP`;
- the mount point is chowned to the workload user when it is created.

## No escalation by calling the API back

A process inside the sandbox can reach the API, and it may hold a valid token.
That grants it nothing extra: every execution surface applies the same
identity — there is no "run as root" parameter — and the filesystem endpoints
are checked by the kernel against the workload user, so the classic escalation
(overwrite a root-owned binary such as `blfs` or `sandbox-api`, wait for a
privileged component to run it) fails with `EACCES`.

Two things are worth stating plainly:

- **The microVM remains the security boundary.** This model contains what a
  compromised workload process can do inside the VM; it is not a substitute for
  the VM isolation, and API authentication still gates everything.
- **Supplementary groups are not applied to filesystem operations.**
  `setfsgid(2)` covers the primary group only, so access granted exclusively
  through a secondary group is denied inside `/filesystem` while it is allowed
  for spawned processes (which do get the full group list).
