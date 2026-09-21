set -eu
adb -s "$(cat /run/android/adb-address)" wait-for-device
adb -s "$(cat /run/android/adb-address)" shell svc power stayon true
adb -s "$(cat /run/android/adb-address)" shell settings put system screen_off_timeout 1800000
adb -s "$(cat /run/android/adb-address)" install -r /work/android/markor.apk
adb -s "$(cat /run/android/adb-address)" shell am start -W -n net.gsantner.markor/.activity.MainActivity
python3 /work/android/ui.py intro

python3 /work/android/ui.py tap DONE
python3 /work/android/ui.py tap OK
python3 /work/android/ui.py tap ALLOW
set -eu
python3 /work/android/ui.py tap 'Allow access to manage all files'
adb -s "$(cat /run/android/adb-address)" shell input keyevent KEYCODE_BACK
sleep 1
python3 /work/android/ui.py dump

python3 /work/android/ui.py tap QuickNote
set -eu
python3 /work/android/ui.py tap net.gsantner.markor:id/document__fragment__edit__highlighting_editor
adb -s "$(cat /run/android/adb-address)" shell input text 'Blaxel%sAndroid%sE2E%spersistence%stest'
adb -s "$(cat /run/android/adb-address)" shell input keyevent KEYCODE_ENTER
adb -s "$(cat /run/android/adb-address)" shell input text 'Created%svia%sauthenticated%sADB%sin%sa%sdev%ssandbox.'
adb -s "$(cat /run/android/adb-address)" shell input keyevent KEYCODE_BACK
python3 /work/android/ui.py tap Save
python3 /work/android/ui.py tap Files
adb -s "$(cat /run/android/adb-address)" shell am force-stop net.gsantner.markor
adb -s "$(cat /run/android/adb-address)" shell am start -W -n net.gsantner.markor/.activity.MainActivity
sleep 1
python3 /work/android/ui.py tap QuickNote
adb -s "$(cat /run/android/adb-address)" shell uiautomator dump /data/local/tmp/verified-note.xml
adb -s "$(cat /run/android/adb-address)" pull /data/local/tmp/verified-note.xml /work/android/verified-note.xml
python3 - <<'PY'
import xml.etree.ElementTree as ET
x=ET.parse('/work/android/verified-note.xml')
values=[n.get('text','') for n in x.iter('node')]
assert any('Blaxel Android E2E persistence test' in v and 'Created via authenticated ADB in a dev sandbox.' in v for v in values),values
print('NOTE_PERSISTENCE_VERIFIED_AFTER_APP_RESTART')
PY
adb -s "$(cat /run/android/adb-address)" exec-out screencap -p > /work/android/verified-note.png
adb -s "$(cat /run/android/adb-address)" shell cat /sdcard/Documents/markor/QuickNote.md

head -5 /proc/meminfo
runc --root /run/android/runc state android

