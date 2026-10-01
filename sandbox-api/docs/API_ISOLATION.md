# API isolation (`BL_SANDBOX_API_ISOLATION`)

Calls that reach the sandbox API through the gateway are authenticated before
they arrive. Calls made from inside the sandbox are not: any process can
`curl localhost:8080/process` and run commands, read files or open terminals.

`BL_SANDBOX_API_ISOLATION=true` closes that path in the kernel. At boot, before
anything of the workload runs, the API installs an nftables table that rejects
(TCP reset) connections to its port from user workloads, while the gateway keeps
working as before.

## Enabling it

```dockerfile
RUN adduser -D -u 10001 -h /blaxel app
USER app

ENV BL_SANDBOX_API_ISOLATION=true
```

The option turns the workload identity on by itself: the image `USER` (exported
by the runtime as `BL_SANDBOX_USER`) applies to processes, terminals and
filesystem operations without `BL_SANDBOX_USER_ENABLED`. See
[UNPRIVILEGED_EXECUTION.md](UNPRIVILEGED_EXECUTION.md) for what that identity
covers. `--user` and `BL_SANDBOX_USER_ENABLED` keep working and take precedence.

## What is refused

The rules live in `table inet blaxel_sandbox_api` (IPv4 and IPv6), shown here
for the default port and an uplink named `eth0`:

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
| Gateway (authenticated), arriving on the uplink | allowed |
| Any root process inside the VM: initrd metadata reload, the API itself, image entrypoints bootstrapping through `localhost:8080` | allowed |
| Any non-root process, on `127.0.0.1`, `::1`, `localhost` or the VM's own address | refused |
| Containers, tunnels or bridges inside the VM (any interface other than loopback and the uplink) | refused |

- Every connection to an address of the VM itself, including its uplink
  address, goes through loopback, so the socket owner's uid tells local callers
  apart. The default guest kernel has nftables but not the iptables `owner`
  match, hence nftables.
- The uplink is the interface of the default route, IPv6 first (the family the
  gateway dials), skipping tunnel devices such as the egress WireGuard.
- Only the API port is filtered. Previews and other ports are untouched.
- The table is replaced, not stacked, when the API restarts or upgrades, and
  removed when it starts with the option off.

## Best effort without a `USER`

Without a `USER` directive, or with `USER root`, the API still installs the
rules, logs a warning and runs the workload as root, as before.

**A root workload can bypass the isolation**: root is allowed by the rules
themselves, and it can delete the table, kill the API, or reach it any other
way a VM's root can. The isolation is only as strong as the workload identity;
it is meant for images whose workload runs as a non-root `USER`. The microVM
remains the security boundary.

## Failure

If the rules cannot be installed (no nftables in the kernel, no default route
to identify the uplink), the API refuses to start: the option was asked for,
and a sandbox silently left open is worse than one that does not start. The one
exception is a restart that finds no default route, which a crashed egress
tunnel can leave behind: the rules of the previous run are still in the kernel
and are kept.
