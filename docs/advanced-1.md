# JetKVM advanced-1

advanced-1 is an unofficial firmware for the JetKVM, based on JetKVM 0.5.11. It is not made,
signed or supported by JetKVM. This page covers only what advanced-1 adds or changes; for
everything else the [original documentation](https://jetkvm.com/docs) applies.

**Hardware:** the standard JetKVM (`jetkvm-v2`) only. The JetKVM Mini is not supported yet.

## Install

1. Download `advanced-1-install.zip` from the newest
   [release](https://github.com/imkebe/kvm/releases) and unpack it.
2. Follow the `README.md` inside. It hands the files to the KVM over your local network, checks the
   hardware and the checksums, keeps your settings and saves your current app on the KVM.

The package has two parts. By default the installer writes both; add `app-only` to its command
to leave the system image as it is.

| Part | What it is | Do you need it? |
| --- | --- | --- |
| App (`jetkvm_app`) | The KVM program and its web interface. Holds all the functions. | Always. |
| System image (`system.tar`) | Boot loader, kernel and root filesystem. | Only for the functions listed below. |

### The system image

The JetKVM has two layers of software: the system image and, on top of it, the app. Almost all of
advanced-1 is in the app. The advanced-1 system image is the official one (0.2.9) with two changes:

- **dnsmasq is included.** The KVM uses it to give the connected computer a network address over
  USB Ethernet. On the official system image USB Ethernet reports that dnsmasq is missing.
- **Spare USB endpoints.** The kernel offers eight HID devices instead of four, and a marker file
  (`/etc/jetkvm-usb-spare-in`) tells the app it may use them to work around unreliable endpoints
  of the USB controller. This is what lets camera, microphone and USB Ethernet be on together.

Everything else works on the official system image.

To see what a KVM runs, open **Settings > General**: it shows the app and the system version. The
advanced-1 system image is `0.2.9-dev202611181148`.

The system image is also a download of its own, in the
[releases of rv1106-system](https://github.com/imkebe/rv1106-system/releases/latest). It is the same
file as the one in the install package.

Before you write the system image, know this:

- It replaces the boot loader. Keep the KVM powered until it has rebooted. What happens after an
  interrupted write was not tested.
- It is never updated automatically. A new system image, if one is released, is installed the same
  way as the first one.
- Going back to the official system image needs JetKVM's own recovery procedure.

Going back to the official app is described in the package's `README.md`.

## Updates

**Settings > General** checks the newest release of this repository. Only the app is updated this
way; the system image is never updated automatically.

Releases are signed with the key `4835 C154 F7B2 85B9 D206 E0AA FFEE 7E83 8E6B BB6B`
("JetKVM advanced releases"). Its public part is built into the firmware
(`internal/ota/update_key.asc`). The KVM refuses an update whose signature does not match. Keep
**Dev Channel Updates** (Settings > Advanced) off: with it on, the signature is not checked.

## Where things are

| Function | Where | Needs |
| --- | --- | --- |
| USB devices, presets, names | Settings > Hardware > USB | |
| USB camera (MJPEG; H.264 is experimental and off by default) | Settings > Hardware > Camera | a browser with a camera |
| USB microphone | Settings > Hardware > Microphone | a browser with a microphone |
| USB Ethernet | Settings > Hardware > Ethernet | the advanced-1 system image |
| NetBird | Settings > Access, section VPN | |
| Folder as a disk | Virtual Media > Folder from this computer | |
| Screenshot and recording | buttons in the top bar | |
| Immersive window, detachable keyboard | top bar | |
| Host info | Host Info in the top bar; switch it on in Settings > OS Services | OS Services from the `advanced-1` branch |
| Terminal on the connected computer | Web Terminal in the top bar | OS Services |
| API tokens, Device API, Terminal API, MCP server | Settings > MCP | |

## Not usable or not advised right now

- **Remote access through the cloud** is not usable. The JetKVM Cloud service is not supported by
  this firmware. To reach the KVM from outside your network, use the NetBird client.
- **Tailscale** was not tested with advanced-1, neither on the KVM nor as a USB Ethernet mode.
  Using it is not advised for now.

A change of the USB devices, or of the camera's size or rate, reconnects all USB devices on the
connected computer for a moment.

## USB devices and the system image

The USB controller of the JetKVM has a fixed number of endpoints, and every USB device uses some
of them. Settings > Hardware > USB shows how many are in use and refuses a combination that does
not fit. Camera, microphone and USB Ethernet together fit only with the advanced-1 system image.

USB Ethernet has four modes: NAT and Routed use the KVM's own network; NetBird makes the connected
computer its own NetBird peer; Tailscale sends its traffic through the KVM's Tailscale link
(not tested, not advised for now).
A firewall on the KVM keeps the connected computer away from the KVM's web page and other
services on this link.

## Folder as a disk

Virtual Media > Folder from this computer shows a folder of your computer to the connected
computer as a USB disk.

- The disk is **read-only**. The connected computer cannot change your files.
- It is a **snapshot**: the list of files is fixed when you mount. Mount again to show changes.
- Nothing is copied to the KVM. File contents are read from your browser when the connected
  computer asks for them, so **keep the tab open**. If the connection drops, the KVM waits a
  minute for the tab to come back, then ejects the disk.
- The file system is **FAT32** or **exFAT**. Auto picks one; you can choose.
  - FAT32 is read by every system and by firmware. A UEFI computer can boot from it when the
    folder holds `EFI/BOOT/BOOTX64.EFI`. Files of 4 GB or more are left out.
  - exFAT is read by Windows, macOS and Linux 5.7 or later, and holds files of any size.
    Firmware cannot boot from it.
- Limits: 2 TiB and 100,000 files.

## Host info and the terminal

Both need [IP-KVM OS Services](https://github.com/imkebe/os-services) on the connected computer.
The terminal works with the official service. Host info needs the service built from the
`advanced-1` branch, and is off until you switch it on in Settings > OS Services.

The [releases of os-services](https://github.com/imkebe/os-services/releases/latest) have that
service for Windows (x64), macOS (Apple silicon and Intel) and Linux (x64, arm64, armhf), and
`jetkvm-os-services.iso`, a disc with all of them. The KVM offers JetKVM's official service by
default. To make it download and mount the advanced-1 disc instead, open **Settings > OS Services >
Download repository**, choose **GitHub repository** and enter `https://github.com/imkebe/os-services`.
These releases are not signed by JetKVM, so the KVM does not update them by itself.

## API / MCP

Everything here is off by default. Switch on what you need in **Settings > MCP** and create an API
token there. A token has the scope `device`, `terminal`, or both, and is sent as
`Authorization: Bearer <token>`. The KVM's password does not open these interfaces.

| Interface | Address | Scope |
| --- | --- | --- |
| Device API (JSON-RPC 2.0) | `POST http://<kvm>/api/rpc` | `device` |
| Terminal API (WebSocket; shells on the connected computer) | `GET http://<kvm>/os-services/terminal/ws` | `terminal` |
| MCP server (streamable HTTP) | `http://<kvm>/mcp` | the tools follow the token's scopes |

Over plain `http` the token crosses your network unencrypted. A token with the scope `terminal`
can run commands on the connected computer as the user of its desktop session.

## Build

```bash
git clone --branch advanced-1 https://github.com/imkebe/kvm.git
cd kvm
make frontend
make build_release VERSION=0.5.11-advanced.1
```

The result is `bin/jetkvm_app`. The system image is built from the `advanced-1` branch of
[rv1106-system](https://github.com/imkebe/rv1106-system).

## State

advanced-1 runs on the author's two devices. The parts that need a browser (camera, microphone,
capture, folder as a disk, immersive window, terminal, the MCP page) were not retested after the
last clean-up of the code. The installer was tested on devices that already had earlier builds;
installing over the official firmware and the step that writes the system image were not tested
this way. Report problems in the
[issues](https://github.com/imkebe/kvm/issues) of this repository, not to JetKVM.
