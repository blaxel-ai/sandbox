# API isolation (`BL_SANDBOX_API_ISOLATION`)

With `BL_SANDBOX_API_ISOLATION=true`, the sandbox API refuses connections from
user workloads running inside the sandbox. Calls through the gateway are not
affected.

At boot, before any workload process starts, the API installs an nftables table
that answers refused connections with a TCP reset.

## Enabling it

```dockerfile
RUN adduser -D -u 10001 -h /blaxel app
USER app

ENV BL_SANDBOX_API_ISOLATION=true
```

The option enables the workload identity: processes, terminals and filesystem
operations run as the image `USER` (exported by the runtime as
`BL_SANDBOX_USER`), without `BL_SANDBOX_USER_ENABLED`. See
[UNPRIVILEGED_EXECUTION.md](UNPRIVILEGED_EXECUTION.md) for what that identity
covers. `--user` and `BL_SANDBOX_USER_ENABLED` take precedence.

## Rules

The rules live in `table inet blaxel_sandbox_api` and apply to IPv4 and IPv6.
For the default port and an uplink named `eth0`:

```
table inet blaxel_sandbox_api {
	chain output {
		type filter hook output priority filter; policy accept;
		oifname "lo" tcp dport 8080 meta skuid != 0 reject with tcp reset
	}

	chain input {
		type filter hook input priority filter; policy accept;
		iifname != "lo" iifname != "eth0" tcp dport 8080 reject with tcp reset
	}
}
```

| Caller | Result |
|---|---|
| Gateway, arriving on the uplink | allowed |
| Root processes inside the VM (initrd metadata reload, image entrypoints calling `localhost:8080`) | allowed |
| Non-root processes, on `127.0.0.1`, `::1`, `localhost` or any address of the VM, including with a socket bound to the uplink | refused |
| Containers, tunnels or bridges inside the VM (any interface other than loopback and the uplink) | refused |

- The uplink is the interface of the IPv6 default route, or else the IPv4 one.
  Tunnel devices, such as the egress WireGuard, are never the uplink.
- Only the API port is filtered. Previews and other ports are untouched.
- Each start replaces the table. Starting with the option off deletes it.

## Without a `USER`

Without a `USER` directive, or with `USER root`, the API installs the rules,
logs a warning and runs the workload as root.

**A root workload can bypass the isolation.** Root callers are allowed by the
rules, and root can also delete the table or stop the API. The isolation only
protects the API from workloads running as a non-root `USER`.

## Failure

The API exits at boot when the rules cannot be installed: nftables missing
from the kernel, or no default route to identify the uplink.

On a restart that finds no default route (for example after the egress tunnel
crashed), the API keeps the table installed by the previous start and boots.
