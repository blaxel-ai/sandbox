---
name: android-e2e
description: Run the Android sandbox E2E script in dev after every modification to hub/android or shared sandbox behavior affecting Android. Verify Android boot, authenticated ADB, app persistence, security boundaries and supervisor restart against the published image.
---

# Android E2E

Run this script in **dev whenever the Android sandbox changes**, before reporting the change as validated. Use a published image containing the changes; a local build or unit tests alone do not establish live behavior.

## Run

Requires Python 3 and an authenticated `bl` CLI. Wait for the Android `build-s3-hub` job for the intended develop commit to succeed, then use its immutable image tag:

```bash
python3 .agents/skills/android-e2e/scripts/run.py \
  --image blaxel/android:develop-<commit-sha> \
  --workspace chris \
  --region us-was-1
```

The script forces `BL_ENV=dev`, ignores local dotenv files, creates a unique sandbox with 1 GiB RAM and an 8 GiB ephemeral root volume, and deletes only that sandbox on exit. It never deploys code or changes workspace settings. The workspace must support mk3.1 and the selected region must provide the Android kernel. HIPAA can force mk3.0 even when the mk3.1 flag is enabled; report that incompatibility if creation fails, without changing flags or HIPAA settings.

The fixtures install a checksum-pinned Markor APK, enter a note through the UI, force-stop and reopen the app, and compare saved file and UI content. They also check authenticated ADB, rejection of keyless ADB, raw block read denial through both runc and ADB, device-filtered service cgroups, and IPv4/IPv6 host and link-local filtering. The link-local probe uses a temporary local sink rather than an external metadata endpoint. Finally, they restart the Android supervisor, verify note/key preservation and repeat the security checks.

## Evidence and failures

Use the output directory printed by the runner for the manifest, process results, screenshot and summary. Report the exact image, workspace, exit status and any failed stage. Inspect `verified-note.png` to confirm the note is visible. A pass requires all stages and cleanup to succeed; a partial run is not an E2E pass.

The runner polls with finite deadlines and progress output. If progress looks abnormal, inspect the named sandbox and current process instead of leaving an unattended wait. On failure, inspect the saved stdout/stderr before retrying with a new sandbox. If deletion fails, use the exact sandbox name in the summary to retry cleanup in dev.

To validate changes to this runner locally:

```bash
python3 -m unittest discover -s .agents/skills/android-e2e/tests -v
```

Then run the full dev command above. Keep the readable commands in `scripts/fixtures/` alongside their execution metadata; update the pinned APK and its checksum together when changing the app version.
