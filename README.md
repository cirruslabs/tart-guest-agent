# Guest agent for Tart VMs

A guest agent for Tart VMS is a lightweight background service that runs inside the virtual machine and enables enhanced communication between the host and guest and other useful features, such as automatic disk resizing.

Currently implemented features:

* Automatic disk resizing for macOS VMs with recovery partition removed (`--resize-disk`)
    * needs to be invoked as a launchd [global daemon](https://launchd.info/)
* Multi-format clipboard sharing for macOS and Linux (X11 / XWayland) VMs using SPICE vdagent implementation (`--run-vdagent`)
    * supports UTF-8 text and images (PNG, TIFF, JPG, BMP) with automatic PNG compression and transcoding
    * on macOS: invoke as a launchd global agent
    * on Linux: invoke as a systemd user service with `DISPLAY` (along with `WAYLAND_DISPLAY` and `XDG_RUNTIME_DIR` if running Wayland with an XWayland bridge, required by `golang.design/x/clipboard`). Ensure user has read/write permission to `/dev/virtio-ports/com.redhat.spice.0` or `/dev/vport*` via device group membership (e.g. `dialout`) or a udev rule (`SUBSYSTEM=="virtio-ports", KERNEL=="vport*", MODE="0666"`)
* SPICE File Transfer Daemon (`VD_AGENT_FILE_XFER_*`)
    * automatically receives files transferred over SPICE into `~/Downloads` with collision deduplication and path traversal sanitization
* `tart exec` support (`--run-rpc`)
    * it's recommended to invoke it as a launchd [global agent](https://launchd.info/) because fewer privileges will be available to commands started via `tart exec`
    * however, you can also invoke it as a launchd [global daemon](https://launchd.info/) or systemd system service if running commands started via `tart exec` as `root` is desired
* `tart ip --resolver=agent` support (`--run-rpc`)
    * allows resolving VM's IP address without relying on DHCP leases and/or an ARP table
* Environment and capability diagnostics (`tart-guest-agent doctor` or `--doctor`)
    * checks SPICE serial port accessibility, Wayland/XWayland display sessions, clipboard backends, system CLI tools, and file transfer download targets with actionable remediation hints

To run all features appropriate for a given context, use component groups:

* `--run-daemon`
    * implies `--resize-disk` 
    * example usage: [`tart-guest-daemon.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-daemon.plist)
* `--run-agent`
    * implies `--run-vdagent --run-rpc` 
    * example usage: [`tart-guest-agent.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-agent.plist)
