"""Supervise a single ReDroid instance inside the sandbox microVM."""
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import signal
import stat
import subprocess
import time

BASE = Path('/opt/android')
STATE = Path('/run/android')
LOG = Path('/var/log/android')
DATA = Path('/var/lib/android/data')
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


def rules(net):
    return [(['-t', 'nat'], 'POSTROUTING', ['-s', str(net), '!', '-o', LINK, '-j', 'MASQUERADE']),
            ([], 'FORWARD', ['-i', LINK, '-j', 'ACCEPT']),
            ([], 'FORWARD', ['-o', LINK, '-m', 'conntrack', '--ctstate', 'RELATED,ESTABLISHED', '-j', 'ACCEPT'])]


def cleanup():
    run(*RUNC, 'delete', '--force', 'android', check=False)
    saved = STATE / 'network.json'
    if saved.exists():
        net = ipaddress.ip_network(json.loads(saved.read_text())['subnet'])
        for table, chain, rule in reversed(rules(net)):
            run('iptables', '-w', '5', *table, '-D', chain, *rule, check=False)
        run('adb', 'disconnect', f'{net.network_address + 2}:5555', check=False)
        saved.unlink()
    run('ip', 'link', 'del', LINK, check=False)
    (STATE / 'ready').unlink(missing_ok=True)
    (STATE / 'adb-address').unlink(missing_ok=True)


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
    for table, chain, rule in rules(net):
        run('iptables', '-w', '5', *table, '-A', chain, *rule)
    return guest + ':5555'


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
