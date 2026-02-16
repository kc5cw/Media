# Run USB Vault as a Background Service

This document explains how to run USB Vault continuously without keeping a terminal tab open.

## macOS (launchd, recommended)

USB Vault includes scripts to install a per-user LaunchAgent.

From the repo root:

```bash
./scripts/macos/install_launchd.sh
```

Optional custom port:

```bash
./scripts/macos/install_launchd.sh 4987
```

This script:

1. Builds `usbvaultd`.
2. Copies web assets.
3. Writes `~/Library/LaunchAgents/com.kc5cw.usbvault.plist`.
4. Starts/restarts the service.

Useful commands:

```bash
./scripts/macos/status_launchd.sh
./scripts/macos/uninstall_launchd.sh --keep-plist   # stop only
./scripts/macos/uninstall_launchd.sh                # stop and remove plist
```

Service logs:

- `~/Library/Logs/USBVault/usbvault-launchd.out.log`
- `~/Library/Logs/USBVault/usbvault-launchd.err.log`

Open UI:

- [http://127.0.0.1:4987](http://127.0.0.1:4987)

## Linux / Raspberry Pi (systemd)

You can run USB Vault with `systemd` for auto-start and restart behavior.

Pi user-level service files are included:

- `scripts/pi/systemd/usbvault.service`
- `scripts/pi/systemd/usbvault-kiosk.service`

Install and enable (user service):

```bash
mkdir -p ~/.config/systemd/user
cp scripts/pi/systemd/usbvault.service ~/.config/systemd/user/
cp scripts/pi/systemd/usbvault-kiosk.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now usbvault.service
systemctl --user enable --now usbvault-kiosk.service
```

Check status/logs:

```bash
systemctl --user status usbvault.service
journalctl --user -u usbvault.service -f
```

## Windows 10/11

Two common options:

1. Task Scheduler (built in)
2. NSSM (Non-Sucking Service Manager)

### Option A: Task Scheduler

1. Build `usbvault.exe`.
2. Create a task with trigger `At log on`.
3. Set action to run `usbvault.exe`.
4. Set working directory to your app folder.
5. Configure restart-on-failure in task settings.

### Option B: NSSM

1. Install NSSM.
2. Create service:

```powershell
nssm install USBVault "C:\path\to\usbvault.exe"
```

3. In NSSM UI set:
   - Startup directory: `C:\path\to\app`
   - Environment variables:
     - `USBVAULT_BIND=127.0.0.1`
     - `USBVAULT_PORT=4987`
     - `USBVAULT_DATA_DIR=C:\USBVault\data`
     - `USBVAULT_WEB_DIR=C:\path\to\web`
4. Start service:

```powershell
nssm start USBVault
```

Open UI:

- [http://127.0.0.1:4987](http://127.0.0.1:4987)

## Upgrade flow (all platforms)

1. Pull latest code.
2. Rebuild binary.
3. Restart service manager (`launchd`, `systemd`, Task Scheduler/NSSM).

On macOS with included scripts:

```bash
git pull origin main
./scripts/macos/install_launchd.sh
```
