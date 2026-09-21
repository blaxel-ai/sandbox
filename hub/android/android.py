"""Supervise a single ReDroid instance inside the sandbox microVM."""
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import signal
import socket
import struct
import stat
import subprocess
import time

BASE = Path('/opt/android')
STATE = Path('/run/android')
LOG = Path('/var/log/android')
DATA = Path('/var/lib/android/data')
ADB_KEYS = Path(os.environ.get('ANDROID_USER_HOME', str(Path.home() / '.android')))
LINK = 'android-host'
RUNC = ['runc', '--root', str(STATE / 'runc'), '--log', str(LOG / 'runc.log')]


def run(*args, check=True, timeout=15):
    return subprocess.run(args, check=check, capture_output=True, text=True, timeout=timeout)


def status(state, **extra):
    tmp = STATE / 'status.tmp'
    tmp.write_text(json.dumps({'state': state, **extra}) + '\n')
    tmp.replace(STATE / 'status.json')


def choose_network(routes):
    """Avoid all existing non-default routes, including broad VPN routes."""
    occupied = [ipaddress.ip_network(r['dst'], strict=False) for r in routes
                if r.get('dst') and r['dst'] != 'default']
    for prefix in ['192.168.240.0/24', '172.30.240.0/24', '10.240.240.0/24']:
        for net in ipaddress.ip_network(prefix).subnets(new_prefix=30):
            if not any(net.overlaps(other) for other in occupied):
                return net
    raise RuntimeError('No unused Android subnet found')


def make_config(template, devices):
    config = json.loads(json.dumps(template))
    config['root']['path'] = str(BASE / 'bundle/rootfs')
    for mount in config['mounts']:
        if mount['destination'] == '/data':
            mount['source'] = str(DATA)
    config['linux']['devices'] = devices
    return config


def rules(net, ipv6=False):
    # Host-originated ADB needs replies, but apps must not initiate connections
    # to the supervisor, including through automatically assigned IPv6 addresses.
    host_input = [([], 'INPUT', ['-i', LINK, '-m', 'conntrack', '--ctstate',
                                'RELATED,ESTABLISHED', '-j', 'ACCEPT']),
                  ([], 'INPUT', ['-i', LINK, '-j', 'DROP'])]
    if ipv6:
        return host_input
    return host_input + [(['-t', 'nat'], 'POSTROUTING', ['-s', str(net), '!', '-o', LINK, '-j', 'MASQUERADE']),
            # Defense in depth for cloud metadata. Tenant network isolation is
            # enforced outside this guest; Android root can change guest rules.
            ([], 'FORWARD', ['-i', LINK, '-d', '169.254.0.0/16', '-j', 'DROP']),
            ([], 'FORWARD', ['-i', LINK, '-j', 'ACCEPT']),
            ([], 'FORWARD', ['-o', LINK, '-m', 'conntrack', '--ctstate', 'RELATED,ESTABLISHED', '-j', 'ACCEPT'])]


def remove_rule(table, chain, rule, binary='iptables'):
    # Delete every matching copy. Only legacy's explicit missing-rule result
    # confirms absence; lock, permission, backend and syntax errors must retry
    # on the next launch with the saved subnet still available.
    command = [binary, '-w', '5', *table, '-D', chain, *rule]
    for _ in range(256):
        result = run(*command, check=False)
        if result.returncode == 0:
            continue
        if (result.returncode == 1 and
                'Bad rule (does a matching rule exist in that chain?)' in result.stderr):
            return
        raise RuntimeError(f'Failed to remove Android firewall rule: {result.stderr.strip()}')
    raise RuntimeError('Android firewall cleanup exceeded 256 duplicate rules')


def cleanup():
    # Readiness must disappear even when a later cleanup operation fails.
    (STATE / 'ready').unlink(missing_ok=True)
    (STATE / 'adb-address').unlink(missing_ok=True)
    try:
        run(*RUNC, 'delete', '--force', 'android', check=False)
        saved = STATE / 'network.json'
        if saved.exists():
            net = ipaddress.ip_network(json.loads(saved.read_text())['subnet'])
            for binary in ['iptables', 'ip6tables']:
                for table, chain, rule in reversed(rules(net, ipv6=binary == 'ip6tables')):
                    remove_rule(table, chain, rule, binary)
            run('adb', 'disconnect', f'{net.network_address + 2}:5555', check=False)
            # Retain state if any preceding deletion failed or timed out.
            saved.unlink()
    finally:
        run('ip', 'link', 'del', LINK, check=False)


def configure_network(pid, net):
    gateway, guest = str(net.network_address + 1), str(net.network_address + 2)
    # Save before mutation so interrupted startup can clean partially added rules.
    (STATE / 'network.json').write_text(json.dumps({'subnet': str(net)}))
    run('ip', 'link', 'add', LINK, 'type', 'veth', 'peer', 'name', 'android-peer')
    run('ip', 'addr', 'add', gateway + '/30', 'dev', LINK)
    run('ip', 'link', 'set', LINK, 'up')
    run('ip', 'link', 'set', 'android-peer', 'netns', str(pid))
    ns = ['nsenter', '-t', str(pid), '-n', 'ip']
    for args in [('link', 'set', 'lo', 'up'),
                 ('link', 'set', 'android-peer', 'name', 'eth0'),
                 ('addr', 'add', guest + '/30', 'dev', 'eth0'),
                 ('link', 'set', 'eth0', 'up'),
                 ('route', 'add', 'default', 'via', gateway)]:
        run(*ns, *args)
    Path('/proc/sys/net/ipv4/ip_forward').write_text('1\n')
    for binary in ['iptables', 'ip6tables']:
        for table, chain, rule in rules(net, ipv6=binary == 'ip6tables'):
            run(binary, '-w', '5', *table, '-A', chain, *rule)
    return guest + ':5555'


def prepare_adb_key():
    # Keep the private key outside Android's mount namespace. The default host
    # ADB location also makes ordinary agent adb commands work without flags.
    ADB_KEYS.mkdir(parents=True, exist_ok=True, mode=0o700)
    ADB_KEYS.chmod(0o700)
    private_key = ADB_KEYS / 'adbkey'
    if not private_key.exists():
        run('adb', 'keygen', str(private_key))
    private_key.chmod(0o600)
    public_key = run('adb', 'pubkey', str(private_key)).stdout.strip()
    if not public_key:
        raise RuntimeError('ADB host public key is empty')
    trusted_keys = BASE / 'bundle/rootfs/adb_keys'
    trusted_keys.write_text(public_key + '\n')
    trusted_keys.chmod(0o644)


def verify_adb_auth(endpoint):
    """Reject keyless ADB before publishing readiness, without trusting a property."""
    host, port = endpoint.rsplit(':', 1)
    command = int.from_bytes(b'CNXN', 'little')
    payload = b'host::\0'
    packet = struct.pack('<6I', command, 0x01000001, 1024 * 1024,
                         len(payload), sum(payload), command ^ 0xffffffff) + payload
    with socket.create_connection((host, int(port)), timeout=5) as connection:
        connection.sendall(packet)
        header = b''
        while len(header) < 24:
            chunk = connection.recv(24 - len(header))
            if not chunk:
                raise RuntimeError('ADB closed before authentication challenge')
            header += chunk
    response, auth_type, _, length, _, magic = struct.unpack('<6I', header)
    auth = int.from_bytes(b'AUTH', 'little')
    if response != auth or auth_type != 1 or length != 20 or magic != (auth ^ 0xffffffff):
        raise RuntimeError('ADB must require host key authentication')


def prepare():
    DATA.mkdir(parents=True, exist_ok=True)
    probe = DATA / '.xattr-probe'
    try:
        probe.touch()
        os.setxattr(probe, 'user.default', b'')
    except OSError as exc:
        raise RuntimeError('Android /data requires user xattrs; attach an ephemeral root volume') from exc
    finally:
        probe.unlink(missing_ok=True)
    prepare_adb_key()
    devices = []
    for path in ['/dev/fuse', '/dev/net/tun', '/dev/dma_heap/system']:
        try:
            info = os.stat(path)
        except FileNotFoundError:
            continue
        if stat.S_ISCHR(info.st_mode):
            devices.append({'path': path, 'type': 'c', 'major': os.major(info.st_rdev),
                            'minor': os.minor(info.st_rdev), 'fileMode': 0o666, 'uid': 0, 'gid': 0})
    template = json.loads((BASE / 'config.json').read_text())
    (BASE / 'bundle/config.json').write_text(json.dumps(make_config(template, devices)))


def boot(timeout=180):
    prepare()
    routes = json.loads(run('ip', '-j', '-4', 'route', 'show', 'table', 'all').stdout)
    net = choose_network(routes)
    with (LOG / 'console.log').open('a') as console:
        subprocess.run([*RUNC, 'create', '--bundle', str(BASE / 'bundle'), 'android'],
                       stdout=console, stderr=console, check=True, timeout=30)
    pid = json.loads(run(*RUNC, 'state', 'android').stdout)['pid']
    endpoint = configure_network(pid, net)
    # Android discovers eth0 at init: networking must precede runc start.
    run(*RUNC, 'start', 'android')
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        state = json.loads(run(*RUNC, 'state', 'android').stdout)
        if state['status'] != 'running':
            raise RuntimeError('Android exited during startup; see console.log')
        result = run(*RUNC, 'exec', 'android', '/system/bin/getprop', 'sys.boot_completed', check=False)
        if result.stdout.strip() == '1':
            verify_adb_auth(endpoint)
            run('adb', 'connect', endpoint)
            run('adb', '-s', endpoint, 'shell', 'true')
            (STATE / 'adb-address').write_text(endpoint + '\n')
            (STATE / 'ready').touch()
            status('ready', adbAddress=endpoint)
            return
        time.sleep(2)
    raise TimeoutError(f'Android did not boot within {timeout}s; see console.log')


def main():
    STATE.mkdir(parents=True, exist_ok=True)
    LOG.mkdir(parents=True, exist_ok=True)
    # Hold the lock for the supervisor lifetime, including cleanup.
    with (STATE / 'lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        def stop(_signum, _frame):
            raise SystemExit(0)
        signal.signal(signal.SIGTERM, stop)
        signal.signal(signal.SIGINT, stop)
        try:
            cleanup()
            status('starting')
            boot()
            while True:
                time.sleep(5)
                if json.loads(run(*RUNC, 'state', 'android').stdout)['status'] != 'running':
                    raise RuntimeError('Android exited; restart the startup command to recover')
        except SystemExit:
            status('stopped')
            raise
        except Exception as exc:
            status('failed', error=str(exc))
            raise
        finally:
            cleanup()


if __name__ == '__main__':
    main()
