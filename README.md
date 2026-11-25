<div align="center">
    <img alt="JetKVM logo" src="https://jetkvm.com/logo-blue.png" height="28">

### KVM advanced-1

[Docs](docs/advanced-1.md) | [Releases](https://github.com/imkebe/kvm/releases) | [Issues](https://github.com/imkebe/kvm/issues) | [Original project](https://github.com/jetkvm/kvm)

</div>

JetKVM is a high-performance, open-source KVM over IP (Keyboard, Video, Mouse) solution designed for efficient remote management of computers, servers, and workstations. Whether you're dealing with boot failures, installing a new operating system, adjusting BIOS settings, or simply taking control of a machine from afar, JetKVM provides the tools to get it done effectively.

**advanced-1** is an unofficial firmware for the JetKVM, based on JetKVM 0.5.11. It is not made, signed or supported by JetKVM; you install it at your own risk. It is for the standard JetKVM (`jetkvm-v2`) only. The JetKVM Mini is not supported yet.

## Features

- **Ultra-low Latency** - 1080p@60FPS video with 30-60ms latency using H.264 encoding. Smooth mouse and keyboard interaction for responsive remote control.
- **Remote Access** - Not usable right now: the JetKVM Cloud service is not supported by this firmware. To reach the KVM from outside your network, use the NetBird client.
- **Optional Tailscale and NetBird Networking** - A NetBird client on the KVM, and the original Tailscale status and control-server configuration, including custom [Headscale](https://headscale.net/)-compatible endpoints. Tailscale was not tested with advanced-1; using it is not advised for now.
- **Bonjour / DNS-SD Discovery** - JetKVM devices advertise themselves as `_jetkvm._tcp` on the local network, with TXT records exposing the firmware version, device ID, and setup state.
- **Open-source software** - Written in Golang on Linux. Easily customizable through SSH access to the JetKVM device.

Added by advanced-1 (details in the [docs](docs/advanced-1.md)):

- **USB camera and microphone** - The camera and the microphone of your browser appear on the connected computer as a USB webcam and a USB microphone. The camera sends MJPEG; H.264 is experimental and off by default.
- **USB Ethernet** - The connected computer gets a network link through the KVM: NAT, routed, as its own NetBird peer, or through Tailscale (not tested, not advised for now). A firewall keeps it away from the KVM's own services. Needs the advanced-1 system image.
- **Virtual media from a folder** - Pick a folder in the browser; the connected computer sees a read-only snapshot of it as a USB disk with a FAT32 or exFAT file system. Nothing is copied to the KVM.
- **USB settings** - Presets, a view of how many of the USB controller's endpoints are in use, your own names for the USB devices, and a jiggler for the absolute mouse.
- **Device screen** - A home screen with status icons and one card per function.
- **Stream capture** - Screenshot and recording of the video stream, saved in the browser.
- **Detachable keyboard and immersive window** - The on-screen keyboard in its own window, and a window that shows the stream alone.
- **Network tools** - Discovery of the KVM's neighbours (also to pick a Wake-on-LAN target), and a static fallback address when DHCP gives none.
- **Host info** - Processor, memory, disks and processes of the connected computer, through [IP-KVM OS Services](https://github.com/imkebe/os-services).
- **Terminal** - A dock with tabs for shells on the connected computer, a choice of shell and terminal multiplexer, and paste as keystrokes.
- **API / MCP** - API tokens, a Device API over HTTP, a Terminal API over WebSocket and an MCP server on the KVM for AI agents. All are off by default.
- **Own updates** - Signed releases from this repository instead of JetKVM's update service.

## Install

advanced-1 has two parts. Both are in `advanced-1-install.zip` on the [releases page](https://github.com/imkebe/kvm/releases), with step-by-step instructions inside.

| Part | What it is | Do you need it? |
| --- | --- | --- |
| App | The KVM program and its web interface. Holds all the functions. | Always. |
| System image | Boot loader, kernel and root filesystem, with two changes for advanced-1. | For USB Ethernet, and to have camera, microphone and USB Ethernet on at the same time. Everything else works on the official system image. |

After the install, the KVM takes app updates from this repository's releases. The system image is never updated automatically. Details, risks and the way back are in the [docs](docs/advanced-1.md#install).

## Contributing

We welcome contributions from the community! Whether it's improving the firmware, adding new features, or enhancing documentation, your input is valuable. We also have some rules and taboos here, so please read this page and our [Code of Conduct](/CODE_OF_CONDUCT.md) carefully.

## I need help

For what advanced-1 adds, read its [documentation](docs/advanced-1.md). For everything else, the original [Documentation](https://jetkvm.com/docs) applies. Please do not ask JetKVM for support with this firmware.

## I want to report an issue

If you've found an issue and want to report it, please check our [Issues](https://github.com/imkebe/kvm/issues) page. Make sure the description contains information about the firmware version you're using, your platform, and a clear explanation of the steps to reproduce the issue.

# Development

JetKVM is written in Go & TypeScript. with some bits and pieces written in C. An intermediate level of Go & TypeScript knowledge is recommended for comfortable programming.

The project contains two main parts, the backend software that runs on the KVM device and the frontend software that is served by the KVM device, and also the cloud.

For comprehensive development information, including setup, testing, debugging, and contribution guidelines, see **[DEVELOPMENT.md](DEVELOPMENT.md)**.

For quick device development, use the `./dev_deploy.sh` script. It will build the frontend and backend and deploy them to the local KVM device. Run `./dev_deploy.sh --help` for more information.

## Backend

The backend is written in Go and is responsible for the KVM device management, the cloud API and the cloud web.

## Frontend

The frontend is written in React and TypeScript and is served by the KVM device. It has three build targets: `device`, `development` and `production`. Development is used for development of the cloud version on your local machine, device is used for building the frontend for the KVM device and production is used for building the frontend for the cloud.
