# Drive UID/GID Mapping

This document describes the optional UID/GID mapping feature for drive mounts, which remaps file ownership between the local sandbox and the filer.

## Overview

When mounting a drive, you can specify a local UID and/or GID to map to filer UID/GID `0`. This is useful when the sandbox process runs as a non-root user but needs to access files owned by root on the filer.

## How It Works

The mapping is passed to the `blfs` FUSE binary as `-map.uid=<local>:0` and `-map.gid=<local>:0` flags. Files owned by UID/GID `0` on the filer will appear as owned by the specified local UID/GID inside the sandbox.

### Resolution Priority

Values are resolved in this order (first non-empty wins):

| Priority | Source | Description |
|----------|--------|-------------|
| 1 | Request parameter | `uidMap` / `gidMap` fields in the mount request body |
| 2 | Environment variable | `BLFS_UID_MAP` / `BLFS_GID_MAP` set on the sandbox |
| 3 | None | No mapping applied (default, backward compatible) |

## API Usage

**POST /drives/mount**

```json
{
  "driveName": "my-drive",
  "mountPath": "/mnt/data",
  "uidMap": "1000",
  "gidMap": "1000"
}
```

This mounts the drive at `/mnt/data` and maps local UID `1000` to filer UID `0`, and local GID `1000` to filer GID `0`.

### Response

The response includes the **effective** UID/GID values that were actually applied (after resolving environment variable defaults):

```json
{
  "success": true,
  "message": "Drive mounted successfully",
  "driveName": "my-drive",
  "mountPath": "/mnt/data",
  "drivePath": "/",
  "readOnly": false,
  "uidMap": "1000",
  "gidMap": "1000"
}
```

## Environment Variable Defaults

Set these environment variables on the sandbox to apply UID/GID mapping to all mounts by default:

| Variable | Description |
|----------|-------------|
| `BLFS_UID_MAP` | Default local UID (e.g., `1000`) |
| `BLFS_GID_MAP` | Default local GID (e.g., `1000`) |

Per-request values always override environment variable defaults.

## Validation

- Values must be **non-negative integers** (e.g., `0`, `1000`, `65534`).
- Negative values and non-numeric strings are rejected with an error.
- The filer side is always `0` (hardcoded).

## Listing Mounts

**GET /drives/mount**

The `uidMap` and `gidMap` fields are included in the response schema but will be empty when listing mounts, since `/proc/mounts` does not expose the UID/GID mapping options.
