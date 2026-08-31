# Guest agent for Tart VMs

A guest agent for Tart VMS is a lightweight background service that runs inside the virtual machine and enables enhanced communication between the host and guest and other useful features, such as automatic disk resizing.

Currently implemented features:

* Automatic disk resizing for macOS VMs with recovery partition removed (`--resize-disk`)
    * needs to be invoked as a launchd [global daemon](https://launchd.info/)
* Clipboard sharing for macOS VMs using our in-house SPICE vdagent implementation (`--run-vdagent`)
    * needs to be invoked as a launchd [global agent](https://launchd.info/)
* `tart exec` support (`--run-rpc`)
    * it's recommended to invoke it as a launchd [global agent](https://launchd.info/) because fewer privileges will be available to commands started via `tart exec`
    * however, you can also invoke it as a launchd [global daemon](https://launchd.info/) if running commands started via `tart exec` as `root` is desired
* `tart ip --resolver=agent` support (`--run-rpc`)
    * allows resolving VM's IP address without relying on DHCP leases and/or an ARP table

To run all features appropriate for a given context, use component groups:

* `--run-daemon`
    * implies `--resize-disk` 
    * example usage: [`tart-guest-daemon.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-daemon.plist)
* `--run-agent`
    * implies `--run-vdagent --run-rpc` 
    * example usage: [`tart-guest-agent.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-agent.plist)

## macOS signing and guest compatibility libraries

Darwin releases retain Hardened Runtime and the `tart-guest-agent` signing
identifier. They allow DYLD environment variables and disable library validation
so image builders can opt the agent into a guest-installed compatibility library,
such as the Tart Metal shim, without re-signing it and changing its TCC identity.
These exceptions permit loading libraries not signed by the agent's developer;
they do not install a library or enable injection by default. Keep any configured
library and launchd plist root-owned and non-writable by untrusted users. The same
binary implements agent and daemon modes, so both carry these entitlements.

Snapshots use the same runtime options and entitlements with an ad-hoc signature.
The existing macOS PR tests build the real agent and check that a hardened binary
rejects injection without the entitlements and accepts it with them. The archive
hook repeats the check against the final signed release executable and checks
entitlements for every architecture. Run it manually with:

```sh
sh packaging/verify-macos-signing.sh /path/to/tart-guest-agent
```
