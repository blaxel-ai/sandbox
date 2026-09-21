set -eu
base64 -w 0 /work/android/verified-note.png > /work/android/verified-note.png.b64
adb -s "$(cat /run/android/adb-address)" pull /sdcard/Documents/markor/QuickNote.md /work/android/QuickNote.md
findmnt -T /data -o TARGET,SOURCE,FSTYPE || true
findmnt -T / -o TARGET,SOURCE,FSTYPE
if command -v docker; then echo DOCKER_PRESENT; else echo NO_DOCKER_BINARY; fi
head -5 /proc/meminfo
runc --root /run/android/runc state android
python3 - <<'PY'
from pathlib import Path
import xml.etree.ElementTree as ET
expected='Blaxel Android E2E persistence test\nCreated via authenticated ADB in a dev sandbox.'
assert Path('/work/android/QuickNote.md').read_text().strip()==expected
assert any(n.get('text')==expected for n in ET.parse('/work/android/verified-note.xml').iter('node'))
print('FILE_AND_UI_NOTE_VERIFIED')
PY

