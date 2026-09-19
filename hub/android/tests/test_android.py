import importlib.util
import ipaddress
import json
from pathlib import Path
import tempfile
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
             patch.object(android, 'run', side_effect=execute), \
             patch.object(android.subprocess, 'run'), \
             patch.object(android, 'configure_network', side_effect=lambda *a: events.append(('network',)) or '192.168.240.2:5555'):
            android.boot()
            start = next(i for i, args in enumerate(events) if 'start' in args)
            self.assertLess(events.index(('network',)), start)
            self.assertEqual(json.loads((Path(d) / 'status.json').read_text())['state'], 'ready')

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
             patch.object(android, 'run') as execute:
            (Path(d) / 'network.json').write_text('{"subnet":"192.168.240.0/30"}')
            (Path(d) / 'ready').touch()
            (Path(d) / 'adb-address').write_text('192.168.240.2:5555')
            android.cleanup()
            commands = [call.args for call in execute.call_args_list]
            self.assertTrue(any('delete' in args and '--force' in args for args in commands))
            self.assertEqual(sum('-D' in args for args in commands), 3)
            self.assertIn(('ip', 'link', 'del', android.LINK), commands)
            self.assertFalse((Path(d) / 'network.json').exists())
            self.assertFalse((Path(d) / 'ready').exists())
            self.assertFalse((Path(d) / 'adb-address').exists())

    def test_xattr_failure_explains_required_volume(self):
        with tempfile.TemporaryDirectory() as d, patch.object(android, 'DATA', Path(d)), \
             patch.object(android.os, 'setxattr', create=True, side_effect=OSError(95, 'unsupported')):
            with self.assertRaisesRegex(RuntimeError, 'ephemeral root volume'):
                android.prepare()
            self.assertFalse((Path(d) / '.xattr-probe').exists())


if __name__ == '__main__':
    unittest.main()
