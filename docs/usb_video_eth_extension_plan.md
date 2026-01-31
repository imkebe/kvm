# JetKVM USB Video & Ethernet Extension – Go Application Plan

## Context & Goals
- Extend the JetKVM Go application and web UI so that the device can expose a USB camera passthrough (WebRTC backed) and a USB Ethernet gadget to controlled hosts.
- Coordinate work across the Go application and firmware/linux distro to deliver a cohesive feature.
- Maintain backward compatibility and predictable behaviour for existing HID and mass storage functions.

## Assumptions
1. Executed commands run from the repository root to preserve relative path expectations.
2. JetKVM target devices are reachable over SSH with developer mode enabled for manual testing.
3. Host build machines have Docker available (or native toolchains) consistent with existing deployment scripts.
4. Firmware images will provide required USB gadget kernel modules (`g_ether`, `uvc`, `configfs`) and allow user services to configure them.
5. Browser clients already support WebRTC + H.264; no additional transcoding is required on the Go side.

## Go Application Implementation Plan

### 1. Feature Flag & Configuration Plumbing
- Add configuration entries (`config.go`, relevant structs) for enabling USB camera passthrough and USB Ethernet gadget.
- Expose toggles in existing device settings APIs and persist within the configuration storage.
- Update validation to ensure mutually exclusive gadget combinations respect USB endpoint limits.

### 2. API Surface Extensions
- Define REST/JSON-RPC endpoints (likely under `cmd/api` or `internal/api`) to manage:
  - Enabling/disabling the USB Ethernet gadget.
  - Querying gadget status (link up, DHCP leases).
  - Initiating camera passthrough sessions (mapping browser camera streams to USB UVC interface).
- Reuse existing authentication/authorization middleware.

### 3. Web UI Integration
- Extend web application state management to surface new toggles.
- Implement front-end flow for browser camera capture:
  - Request local camera permission.
  - Establish WebRTC peer connection to Go backend.
  - Forward captured stream metadata (format, resolution) to firmware service.
- Provide status indicators for USB Ethernet (IP, link state, errors).

### 4. Service Coordination Layer
- Introduce new internal package (e.g., `internal/gadget`) that proxies requests to firmware-side control endpoints (likely over gRPC/REST or dbus-style socket exposed by linux firmware).
- Implement command execution wrappers for developer builds to manually trigger gadget scripts over SSH, ensuring eventual integration with firmware daemon once available.

### 5. Telemetry & Monitoring
- Extend Prometheus metrics (if enabled) to report gadget enablement, session counts, error counters.
- Ensure logs include meaningful context for debugging (USB mode changes, NAT status, WebRTC session IDs).

### 6. Testing Strategy
- Unit tests for configuration parsing, API handlers, and gadget coordination layer using mocks for firmware RPCs.
- Integration tests (where feasible) to exercise WebRTC negotiation paths with in-memory peers.
- Manual QA checklist for verifying USB enumeration on Windows/macOS/Linux once firmware components are in place.

## Deliverables for Go App Scope
- Updated configuration schema and migration handling.
- New API endpoints and associated documentation (`DEVELOPMENT.md` or API reference).
- Front-end controls and UX for camera passthrough and USB Ethernet status.
- Internal package scaffolding to communicate with firmware gadget service (including mocks/stubs).
- Test coverage updates and developer scripts to aid local verification.

# Firmware/Linux High-Level Design

## Overview
Firmware changes enable JetKVM’s Linux distro to expose new USB gadget functions: a UVC device backed by the browser-originated video stream, and a CDC/RNDIS/NCM network interface providing routed connectivity. The firmware must surface control hooks consumable by the Go application.

## Architecture Components

1. **USB Gadget Composition Manager**
   - Extend existing configfs-based gadget setup to include:
     - HID endpoints (keyboard/mouse) as today.
     - Mass storage function (mountable on-demand).
     - **New UVC function**: exposes a virtual camera endpoint, with streaming buffers sourced from user-space daemon.
     - **New Ethernet function**: utilize `usb_f_ecm` + `usb_f_rndis` (via `g_ether`) or `usb_f_ncm` for Windows/macOS/Linux compatibility.
   - Provide a DBus/gRPC/socket API for enabling/disabling optional functions at runtime based on Go app requests.

2. **Media Bridge Service**
   - User-space daemon written in Rust/C++/Go that:
     - Receives WebRTC media frames from Go app (over shared memory, gRPC streaming, or QUIC).
     - Feeds frames into UVC gadget buffer using v4l2loopback or `uvc-gadget` utilities without re-encoding.
     - Manages frame timing and format negotiation (e.g., MJPEG/YUY2) to satisfy host OS camera expectations.

3. **USB Ethernet Networking Stack**
   - Bring up `usb0` interface with static IP (e.g., 172.30.30.1/24).
   - Run lightweight DHCP (dnsmasq/udhcpd) to assign addresses and advertise DNS.
   - Enable IPv4 forwarding and NAT (iptables/nftables) toward `eth0` or active uplink.
   - Optionally expose configuration via systemd units or kvmd-style otgnet scripts.
   - Provide status metrics (link state, connected hosts) accessible via the control API.

4. **Security & Resource Controls**
   - Update firewall rules to permit DHCP, ICMP, and optional web/SSH access over usb0 while limiting unsolicited inbound traffic.
   - Ensure USB endpoint allocation respects hardware limits; gracefully degrade (e.g., disable mass storage when Ethernet + UVC active) with error codes returned to Go app.
   - Enforce access control on control APIs (authenticated IPC, privilege separation).

5. **Boot & Persistence**
   - Ship new systemd services or init scripts to:
     - Load necessary kernel modules (`libcomposite`, `usb_f_ecm`, `usb_f_ncm`, `usb_f_uvc`).
     - Mount configfs and instantiate gadget on boot with default profile.
     - Start DHCP/NAT services conditionally based on configuration stored in persistent partition.
   - Integrate with OTA update mechanism ensuring rollback safety.

6. **Diagnostics & Observability**
   - Expose logs via journal and forward summarized status to Go app.
   - Provide CLI tooling (e.g., `jetkvm-gadget status`) for support teams to inspect gadget state.
   - Include self-tests verifying host enumeration for both USB Ethernet and UVC functions.

## Interface with Go Application
- Define a protobuf/REST schema for commands:
  - `SetGadgetProfile { ethernet_enabled, camera_enabled }`
  - `QueryGadgetStatus { usb_endpoints, ethernet: { link_up, client_ip }, camera: { format, fps } }`
  - `PushCameraFrame` or shared buffer management instructions.
- Ensure robust error propagation (USB limit reached, module missing, firmware outdated) so UI can guide users.

## Risks & Mitigations
- **USB Bandwidth/Endpoint Limits**: Validate combined HID + mass storage + UVC + Ethernet fits within controller capabilities; provide priority rules.
- **WebRTC ⇔ UVC Format mismatch**: Implement format conversion pipeline (e.g., using GStreamer) if host requires MJPEG but browser sends NV12; fallback to supported subset.
- **Security Exposure**: NAT’d interface could bridge sensitive networks—apply firewall defaults and document best practices.
- **Driver Availability on Windows**: Consider supporting CDC-NCM to avoid unsigned RNDIS INF requirements.

## Testing Strategy
- Automated tests using USB gadget emulation (usbip, dummy_hcd) in CI for enumeration validation.
- Hardware-in-the-loop tests with Windows/macOS/Linux hosts verifying camera preview and network throughput (>10 Mb/s).
- Stress tests for concurrent camera streaming and network transfers to ensure SoC performance is adequate.

