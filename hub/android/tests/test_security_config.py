import json
from pathlib import Path
import unittest


class DevicePolicyTests(unittest.TestCase):
    def test_device_policy_denies_block_access_without_fixed_binder_numbers(self):
        config = json.loads((Path(__file__).resolve().parents[1] / 'config.json').read_text())
        rules = config['linux']['resources']['devices']
        self.assertEqual(rules[0], {'allow': False, 'access': 'rwm'})
        allowed = [rule for rule in rules if rule['allow']]
        self.assertTrue(allowed)
        for rule in allowed:
            self.assertEqual(rule.get('type'), 'c', 'Raw guest disks must not be accessible')
        self.assertIn({'allow': True, 'type': 'c', 'access': 'rwm'}, allowed,
                      'Binder allocates device numbers dynamically during Android init')


if __name__ == '__main__':
    unittest.main()
