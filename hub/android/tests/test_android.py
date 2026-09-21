import importlib.util
import ipaddress
import json
from pathlib import Path
import tempfile
import subprocess
import struct
from types import SimpleNamespace
import unittest
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[1] / 'android.py'
spec = importlib.util.spec_from_file_location('android', MODULE)
android = importlib.util.module_from_spec(spec)
spec.loader.exec_module(android)


class AndroidTests(unittest.TestCase):
    def test_network_skips_routes_and_default(self):
        net = android.choose_network([{'dst': 'default'}, {'dst': '192.168.240.0/30'}])
        self.assertEqual(str(net), '192.168.240.4/30')

    def test_network_rejects_all_overlapping_ranges(self):
        with self.assertRaisesRegex(RuntimeError, 'No unused'):
            android.choose_network([{'dst': '192.168.0.0/16'}, {'dst': '172.16.0.0/12'}, {'dst': '10.0.0.0/8'}])

    def test_forwarded_link_local_traffic_is_dropped_before_egress_accept(self):
        rules = android.rules(ipaddress.ip_network('192.168.240.0/30'))
        forward = [rule for table, chain, rule in rules if chain == 'FORWARD']
        drop = ['-i', android.LINK, '-d', '169.254.0.0/16', '-j', 'DROP']
        accept = ['-i', android.LINK, '-j', 'ACCEPT']
        self.assertIn(drop, forward)
        self.assertLess(forward.index(drop), forward.index(accept))

    def test_host_input_allows_adb_replies_before_dropping_new_app_connections(self):
        for ipv6 in [False, True]:
            with self.subTest(ipv6=ipv6):
                inputs = [rule for _, chain, rule in android.rules(
                    ipaddress.ip_network('192.168.240.0/30'), ipv6=ipv6) if chain == 'INPUT']
                self.assertEqual(inputs, [
                    ['-i', android.LINK, '-m', 'conntrack', '--ctstate',
                     'RELATED,ESTABLISHED', '-j', 'ACCEPT'],
                    ['-i', android.LINK, '-j', 'DROP']])

    def test_adb_auth_rejects_keyless_connect_and_accepts_fragmented_challenge(self):
        for response, auth_type, length, accepted in [
                (b'AUTH', 1, 20, True), (b'CNXN', 0x01000001, 6, False),
                (b'AUTH', 2, 20, False)]:
            with self.subTest(response=response, auth_type=auth_type):
                command = int.from_bytes(response, 'little')
                header = struct.pack('<6I', command, auth_type, 0, length, 0,
                                     command ^ 0xffffffff)
                with patch.object(android.socket, 'create_connection') as connect:
                    connection = connect.return_value.__enter__.return_value
                    connection.recv.side_effect = [header[:7], header[7:]]
                    if accepted:
                        android.verify_adb_auth('192.168.240.2:5555')
                    else:
                        with self.assertRaisesRegex(RuntimeError, 'host key authentication'):
                            android.verify_adb_auth('192.168.240.2:5555')
                    self.assertEqual(connection.sendall.call_args.args[0][:4], b'CNXN')

    def test_adb_auth_rejects_connection_closed_without_challenge(self):
        with patch.object(android.socket, 'create_connection') as connect:
            connect.return_value.__enter__.return_value.recv.return_value = b''
            with self.assertRaisesRegex(RuntimeError, 'closed before authentication'):
                android.verify_adb_auth('192.168.240.2:5555')

    def test_adb_private_key_stays_outside_guest_and_survives_restart(self):
        with tempfile.TemporaryDirectory() as d:
            base, keys = Path(d) / 'android', Path(d) / 'host-keys'
            (base / 'bundle/rootfs').mkdir(parents=True)
            def execute(*args, **kwargs):
                if args[1] == 'keygen':
                    Path(args[2]).write_text('private-key')
                return SimpleNamespace(stdout='public-key host')
            with patch.object(android, 'BASE', base), patch.object(android, 'ADB_KEYS', keys), \
                 patch.object(android, 'run', side_effect=execute) as run:
                android.prepare_adb_key()
                android.prepare_adb_key()
                self.assertEqual(sum(call.args[1] == 'keygen' for call in run.call_args_list), 1)
                self.assertEqual((base / 'bundle/rootfs/adb_keys').read_text(), 'public-key host\n')
                self.assertEqual((keys / 'adbkey').stat().st_mode & 0o777, 0o600)
                self.assertEqual(keys.stat().st_mode & 0o777, 0o700)
                self.assertFalse((base / 'bundle/rootfs/adbkey').exists())

    def test_config_uses_runtime_devices_and_private_namespaces(self):
        source = json.loads((MODULE.parent / 'config.json').read_text())
        devices = [{'path': '/dev/dma_heap/system', 'major': 254, 'minor': 7}]
        result = android.make_config(source, devices)
        self.assertEqual(result['linux']['devices'], devices)
        self.assertEqual(result['root']['path'], '/opt/android/bundle/rootfs')
        self.assertTrue(all('path' not in n for n in result['linux']['namespaces']))
        self.assertIn({'type': 'network'}, result['linux']['namespaces'])
        self.assertEqual(source['root']['path'], 'rootfs')

    def test_network_is_created_before_start(self):
        from types import SimpleNamespace
        events = []
        def execute(*args, **kwargs):
            events.append(args)
            if args[:2] == ('ip', '-j'):
                return SimpleNamespace(stdout='[]')
            if 'state' in args:
                return SimpleNamespace(stdout='{"pid":42,"status":"running"}')
            return SimpleNamespace(stdout='1')
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'LOG', Path(d)), \
             patch.object(android, 'STATE', Path(d)), patch.object(android, 'prepare'), \
             patch.object(android, 'verify_adb_auth'), \
             patch.object(android, 'run', side_effect=execute), \
             patch.object(android.subprocess, 'run'), \
             patch.object(android, 'configure_network', side_effect=lambda *a: events.append(('network',)) or '192.168.240.2:5555'):
            android.boot()
            start = next(i for i, args in enumerate(events) if 'start' in args)
            self.assertLess(events.index(('network',)), start)
            self.assertEqual(json.loads((Path(d) / 'status.json').read_text())['state'], 'ready')

    def test_boot_does_not_connect_or_publish_readiness_for_keyless_adb(self):
        def execute(*args, **kwargs):
            if args[:2] == ('ip', '-j'):
                return SimpleNamespace(stdout='[]')
            if 'state' in args:
                return SimpleNamespace(stdout='{"pid":42,"status":"running"}')
            return SimpleNamespace(stdout='1')
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'LOG', Path(d)), \
             patch.object(android, 'STATE', Path(d)), patch.object(android, 'prepare'), \
             patch.object(android, 'run', side_effect=execute) as run, \
             patch.object(android.subprocess, 'run'), \
             patch.object(android, 'configure_network', return_value='192.168.240.2:5555'), \
             patch.object(android, 'verify_adb_auth',
                          side_effect=RuntimeError('ADB must require host key authentication')):
            with self.assertRaisesRegex(RuntimeError, 'host key authentication'):
                android.boot()
            self.assertFalse(any(call.args[0] == 'adb' for call in run.call_args_list))
            self.assertFalse((Path(d) / 'ready').exists())
            self.assertFalse((Path(d) / 'adb-address').exists())

    def test_failed_boot_is_observable_and_cleaned(self):
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'STATE', Path(d)), \
             patch.object(android, 'LOG', Path(d)), patch.object(android, 'cleanup') as cleanup, \
             patch.object(android.signal, 'signal'), \
             patch.object(android, 'boot', side_effect=TimeoutError('boot timed out')):
            with self.assertRaises(TimeoutError):
                android.main()
            self.assertEqual(cleanup.call_count, 2)
            self.assertEqual(json.loads((Path(d) / 'status.json').read_text()),
                             {'state': 'failed', 'error': 'boot timed out'})

    def test_normal_termination_publishes_stopped_status(self):
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'STATE', Path(d)), \
             patch.object(android, 'LOG', Path(d)), patch.object(android, 'cleanup') as cleanup, \
             patch.object(android.signal, 'signal'), \
             patch.object(android, 'boot', side_effect=SystemExit(0)):
            with self.assertRaises(SystemExit):
                android.main()
            self.assertEqual(cleanup.call_count, 2)
            self.assertEqual(json.loads((Path(d) / 'status.json').read_text()), {'state': 'stopped'})

    def test_cleanup_removes_partial_startup_and_stale_readiness(self):
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'STATE', Path(d)), \
             patch.object(android, 'run', return_value=SimpleNamespace(returncode=1, stderr='Bad rule (does a matching rule exist in that chain?)')) as execute:
            (Path(d) / 'network.json').write_text('{"subnet":"192.168.240.0/30"}')
            (Path(d) / 'ready').touch()
            (Path(d) / 'adb-address').write_text('192.168.240.2:5555')
            android.cleanup()
            commands = [call.args for call in execute.call_args_list]
            self.assertTrue(any('delete' in args and '--force' in args for args in commands))
            self.assertEqual(sum('-D' in args for args in commands), 8)
            self.assertIn(('iptables', '-w', '5', '-D', 'FORWARD', '-i', android.LINK,
                           '-d', '169.254.0.0/16', '-j', 'DROP'), commands)
            for binary in ['iptables', 'ip6tables']:
                self.assertIn((binary, '-w', '5', '-D', 'INPUT', '-i',
                               android.LINK, '-j', 'DROP'), commands)
            self.assertIn(('ip', 'link', 'del', android.LINK), commands)
            self.assertFalse((Path(d) / 'network.json').exists())
            self.assertFalse((Path(d) / 'ready').exists())
            self.assertFalse((Path(d) / 'adb-address').exists())

    def test_remove_rule_deletes_all_duplicates_then_accepts_absence(self):
        with patch.object(android, 'run', side_effect=[
            SimpleNamespace(returncode=0, stderr=''),
            SimpleNamespace(returncode=0, stderr=''),
            SimpleNamespace(returncode=1, stderr='iptables: Bad rule (does a matching rule exist in that chain?).'),
        ]) as execute:
            android.remove_rule([], 'FORWARD', ['-i', android.LINK, '-j', 'ACCEPT'])
            self.assertEqual(execute.call_count, 3)
            self.assertEqual(execute.call_args_list[0], execute.call_args_list[2])

    def test_cleanup_retains_state_for_operational_firewall_failures(self):
        for code, error in [(4, 'Another app is currently holding the xtables lock'),
                            (3, 'Permission denied'), (1, 'Unexpected backend failure')]:
            with self.subTest(error=error), tempfile.TemporaryDirectory() as d, \
                 patch.object(android, 'STATE', Path(d)), \
                 patch.object(android, 'run', return_value=SimpleNamespace(returncode=code, stderr=error)) as execute:
                saved = Path(d) / 'network.json'
                saved.write_text('{"subnet":"192.168.240.0/30"}')
                (Path(d) / 'ready').touch()
                with self.assertRaisesRegex(RuntimeError, 'Failed to remove'):
                    android.cleanup()
                self.assertTrue(saved.exists())
                self.assertFalse((Path(d) / 'ready').exists())
                self.assertIn(('ip', 'link', 'del', android.LINK), [call.args for call in execute.call_args_list])

    def test_cleanup_retains_state_after_firewall_timeout(self):
        def execute(*args, **kwargs):
            if args[0] == 'iptables':
                raise subprocess.TimeoutExpired(args, 15)
            return SimpleNamespace(returncode=0, stderr='')
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'STATE', Path(d)), \
             patch.object(android, 'run', side_effect=execute):
            saved = Path(d) / 'network.json'
            saved.write_text('{"subnet":"192.168.240.0/30"}')
            with self.assertRaises(subprocess.TimeoutExpired):
                android.cleanup()
            self.assertTrue(saved.exists())

    def test_failed_cleanup_prevents_boot_overwriting_saved_network(self):
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'STATE', Path(d)), \
             patch.object(android, 'LOG', Path(d)), patch.object(android.signal, 'signal'), \
             patch.object(android, 'cleanup', side_effect=RuntimeError('firewall busy')), \
             patch.object(android, 'boot') as boot:
            with self.assertRaisesRegex(RuntimeError, 'firewall busy'):
                android.main()
            boot.assert_not_called()
            self.assertEqual(json.loads((Path(d) / 'status.json').read_text())['state'], 'failed')

    def test_xattr_failure_explains_required_volume(self):
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'DATA', Path(d)), \
             patch.object(android.os, 'setxattr', create=True, side_effect=OSError(95, 'unsupported')):
            with self.assertRaisesRegex(RuntimeError, 'ephemeral root volume'):
                android.prepare()
            self.assertFalse((Path(d) / '.xattr-probe').exists())


if __name__ == '__main__':
    unittest.main()
