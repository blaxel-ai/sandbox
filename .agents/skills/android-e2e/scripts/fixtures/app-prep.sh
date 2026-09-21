set -eu
mkdir -p /work/android
python3 - <<'PY'
import urllib.request,hashlib,pathlib
url='https://github.com/gsantner/markor/releases/download/v2.16.1/net.gsantner.markor-v163-2.16.1-flavorDefault-release.apk'
data=urllib.request.urlopen(url,timeout=60).read()
assert hashlib.sha256(data).hexdigest()=='e88cdcced7aa3dca25e6b9c7a9bdcfad3e3988ee545be951f42bf9441b5e46bf'
pathlib.Path('/work/android/markor.apk').write_bytes(data)
print('Verified Markor APK',len(data))
PY

cat > /work/android/ui.py <<'UI_HELPER'
import subprocess,xml.etree.ElementTree as ET,re,sys,time
from pathlib import Path
ADB=['adb','-s',Path('/run/android/adb-address').read_text().strip()]
def adb(*args):return subprocess.check_output(ADB+list(args),timeout=25).decode(errors='replace')
def tree():
 adb('shell','uiautomator','dump','/data/local/tmp/window.xml')
 return ET.fromstring(adb('shell','cat','/data/local/tmp/window.xml'))
def show(t):
 for n in t.iter('node'):
  a=n.attrib
  if a.get('text') or a.get('content-desc') or a.get('clickable')=='true':print({k:a.get(k) for k in ['text','content-desc','resource-id','class','bounds','clickable']})
def tap(t,target):
 for n in t.iter('node'):
  a=n.attrib
  if target in [a.get('text'),a.get('content-desc'),a.get('resource-id')]:
   x,y,x2,y2=map(int,re.findall(r'\d+',a['bounds']));assert x2>x and y2>y
   adb('shell','input','tap',str((x+x2)//2),str((y+y2)//2));return True
 return False
if sys.argv[1]=='intro':
 for i in range(8):
  t=tree();show(t)
  if not tap(t,'NEXT'):break
  time.sleep(.5)
elif sys.argv[1]=='tap':
 assert tap(tree(),sys.argv[2]),'Target not found';time.sleep(.6);show(tree())
else:show(tree())

UI_HELPER

