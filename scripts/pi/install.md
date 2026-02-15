# Raspberry Pi install (manual)

## 1) Build binaries on your Mac

```bash
cd /Users/curtishays/BusinessPlan
./scripts/pi/build.sh 0.2.0
```

This creates binaries under `dist/pi/`.

## 2) Copy files to your Pi

Pick the matching architecture binary.

Examples:

```bash
# arm64 Pi OS
scp dist/pi/usbvaultd_linux_arm64_0.2.0 pi@raspberrypi:/tmp/usbvaultd
scp dist/pi/usbvault-kiosk_linux_arm64_0.2.0 pi@raspberrypi:/tmp/usbvault-kiosk

# copy web assets too
scp -r web pi@raspberrypi:/tmp/usbvault-web
```

## 3) Install on the Pi

```bash
sudo install -m 0755 /tmp/usbvaultd /usr/local/bin/usbvaultd
sudo install -m 0755 /tmp/usbvault-kiosk /usr/local/bin/usbvault-kiosk

sudo mkdir -p /usr/local/share/usbvault
sudo rsync -a --delete /tmp/usbvault-web/ /usr/local/share/usbvault/web/
```

Run the server (headless or before systemd):

```bash
USBVAULT_WEB_DIR=/usr/local/share/usbvault/web usbvaultd
```

Open `http://127.0.0.1:4987` on the Pi.

## 4) Enable systemd services (optional)

```bash
mkdir -p ~/.config/systemd/user
cp scripts/pi/systemd/usbvault.service ~/.config/systemd/user/
cp scripts/pi/systemd/usbvault-kiosk.service ~/.config/systemd/user/

systemctl --user daemon-reload
systemctl --user enable --now usbvault.service
systemctl --user enable --now usbvault-kiosk.service
```

Notes:

- `usbvault-kiosk` detects HDMI/DSI/framebuffer and touch input.
- If HDMI is connected it launches the standard UI.
- If a touch device is present and HDMI is not connected, it launches the touch UI.
- Many “GPIO tiny screens” are SPI/DSI and still appear to Linux as a framebuffer device; GPIO itself does not carry video.
