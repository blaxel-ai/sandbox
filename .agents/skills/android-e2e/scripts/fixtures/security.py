import ipaddress
import json
import os
from pathlib import Path
import shlex
import subprocess


def run(*args, check=True):
    result = subprocess.run(args, text=True, capture_output=True, timeout=30)
    if check and result.returncode:
        raise RuntimeError(f"{args}: {result.stderr}")
    return result


for binary, table, chain, expected in [
    ("iptables", "filter", "INPUT", 2),
    ("iptables", "filter", "FORWARD", 3),
    ("iptables", "nat", "POSTROUTING", 1),
    ("ip6tables", "filter", "INPUT", 2),
]:
    entries = [
        line
        for line in run(binary, "-t", table, "-S", chain).stdout.splitlines()
        if "android-host" in shlex.split(line)
    ]
    assert len(entries) == expected, (binary, chain, entries)
print("NO_DUPLICATE_ANDROID_FIREWALL_RULES", flush=True)

config = json.loads(Path("/opt/android/bundle/config.json").read_text())
assert {"type": "cgroup"} in config["linux"]["namespaces"]
assert config["linux"]["resources"]["devices"] == [
    {"allow": False, "access": "rwm"},
    {"allow": True, "type": "c", "access": "rwm"},
]
runc = ["runc", "--root", "/run/android/runc"]
state = json.loads(run(*runc, "state", "android").stdout)
assert state["status"] == "running"
parent_cgroup = Path(f"/proc/{state['pid']}/cgroup").read_text().strip().split("::")[1]
services = {}
for process in Path("/proc").iterdir():
    if not process.name.isdigit():
        continue
    try:
        name = (process / "comm").read_text().strip()
        if name in ["adbd", "system_server"]:
            group = (process / "cgroup").read_text().strip().split("::")[1]
            assert group.startswith(parent_cgroup + "/"), (name, group, parent_cgroup)
            services[name] = group
    except FileNotFoundError:
        pass
assert set(services) == {"adbd", "system_server"}, services
print("ANDROID_SERVICES_STAY_IN_FILTERED_CGROUP", services, flush=True)
endpoint = Path("/run/android/adb-address").read_text().strip()
assert (
    run("adb", "-s", endpoint, "shell", "getprop", "sys.boot_completed").stdout.strip()
    == "1"
)
run("adb", "-s", endpoint, "shell", "true")
run(*runc, "exec", "android", "/system/bin/sh", "-c", "echo char-device-ok > /dev/null")
print("AUTHENTICATED_ADB_BOOT_AND_CHAR_DEVICE_VERIFIED", flush=True)

# runc permits mknod by default. Test actual reads, including Android service cgroups.
device = os.stat("/dev/vda").st_rdev
node = "/data/local/tmp/e2e-block-open-probe"
try:
    run(
        *runc,
        "exec",
        "android",
        "/system/bin/sh",
        "-c",
        f"mknod {node} b {os.major(device)} {os.minor(device)} && chmod 666 {node}",
    )
    commands = [
        runc + ["exec", "android", "/system/bin/dd"],
        ["adb", "-s", endpoint, "shell", "dd"],
    ]
    for command in commands:
        result = run(
            *command, "if=" + node, "of=/dev/null", "bs=1", "count=1", check=False
        )
        assert result.returncode != 0 and "Operation not permitted" in result.stderr, (
            command,
            result.returncode,
            result.stderr,
        )
        print("BLOCK_READ_DENIED", command[0], flush=True)
finally:
    run(*runc, "exec", "android", "/system/bin/rm", "-f", node)


def in_network(script):
    return run(
        "nsenter", "-t", str(state["pid"]), "-n", "python3", "-c", script
    ).stdout.strip()


probe = r"""
import socket,struct
with socket.create_connection(('127.0.0.1',5555),timeout=5) as connection:
 payload=b'host::\0'
 command=int.from_bytes(b'CNXN','little')
 connection.sendall(struct.pack('<6I',command,0x01000001,4096,len(payload),sum(payload),command^0xffffffff)+payload)
 header=b''
 while len(header)<24:
  chunk=connection.recv(24-len(header))
  assert chunk, 'ADB closed without challenge'
  header+=chunk
 response,kind,_,length,_,magic=struct.unpack('<6I',header)
 auth=int.from_bytes(b'AUTH','little')
 assert (response,kind,length,magic)==(auth,1,20,auth^0xffffffff)
print('KEYLESS_ADB_AUTH_CHALLENGE_VERIFIED')
"""
print(in_network(probe), flush=True)


def drop_packets(binary, chain, destination=None):
    listing = run(binary, "-nvxL", chain).stdout
    for line in listing.splitlines():
        fields = line.split()
        if len(fields) > 6 and fields[2] == "DROP" and "android-host" in fields:
            if destination is None or destination in fields:
                return int(fields[0])
    raise AssertionError("Missing DROP: " + listing)


network = ipaddress.ip_network(
    json.loads(Path("/run/android/network.json").read_text())["subnet"]
)
gateway = str(network.network_address + 1)
before = drop_packets("iptables", "INPUT")
print(
    in_network(
        "import socket; s=socket.socket(); s.settimeout(2); result=s.connect_ex(("
        + repr(gateway)
        + ",8080)); assert result!=0; print('IPV4_HOST_API_BLOCKED',result)"
    ),
    flush=True,
)
after = drop_packets("iptables", "INPUT")
assert after > before, ("IPv4 INPUT", before, after)
print("IPV4_INPUT_DROP_INCREMENT", after - before, flush=True)
addresses = json.loads(
    run("ip", "-j", "-6", "addr", "show", "dev", "android-host").stdout
)
ipv6 = [
    a["local"]
    for iface in addresses
    for a in iface.get("addr_info", [])
    if a["family"] == "inet6"
]
assert ipv6, "No IPv6 host address to test"
before = drop_packets("ip6tables", "INPUT")
print(
    in_network(
        "import socket; s=socket.socket(socket.AF_INET6); s.settimeout(2); result=s.connect_ex(("
        + repr(ipv6[0])
        + ",8080,0,socket.if_nametoindex('eth0'))); assert result!=0; print('IPV6_HOST_API_BLOCKED',result)"
    ),
    flush=True,
)
after = drop_packets("ip6tables", "INPUT")
assert after > before, ("IPv6 INPUT", before, after)
print("IPV6_INPUT_DROP_INCREMENT", after - before, flush=True)

# Route only a synthetic test address to a temporary local veth sink.
# No probe can reach an external metadata endpoint, even if the DROP is broken.
run("ip", "link", "add", "e2e-out", "type", "veth", "peer", "name", "e2e-sink")
try:
    run("ip", "link", "set", "e2e-out", "up")
    run("ip", "link", "set", "e2e-sink", "up")
    run("ip", "route", "add", "169.254.123.123/32", "dev", "e2e-out")
    before = drop_packets("iptables", "FORWARD", "169.254.0.0/16")
    result = run(
        "adb",
        "-s",
        endpoint,
        "shell",
        "ping",
        "-c",
        "1",
        "-W",
        "2",
        "169.254.123.123",
        check=False,
    )
    after = drop_packets("iptables", "FORWARD", "169.254.0.0/16")
    assert result.returncode != 0, (
        "Synthetic link-local destination unexpectedly answered"
    )
    assert after > before, (
        "Link-local FORWARD",
        before,
        after,
        result.stdout,
        result.stderr,
    )
    print("LINK_LOCAL_FORWARD_DROP_INCREMENT", after - before, flush=True)
finally:
    run("ip", "link", "delete", "e2e-out")
print("SECURITY_CHECKS_PASSED", flush=True)
