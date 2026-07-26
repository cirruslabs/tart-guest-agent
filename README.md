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
    * attached `/Agent/Exec` commands report `Started` with the guest process ID before standard output, standard error, or exit
    * clients can send a `Signal` with a unique, nonzero `request_id` and a guest-native signal number; `all = false` signals only that command, while `all = true` signals only its dedicated process group
    * a matching `SignalAck` is sent only after successful delivery; existing clients can ignore the additive events, and detached commands retain their original exit-only behavior
* `tart ip --resolver=agent` support (`--run-rpc`)
    * allows resolving VM's IP address without relying on DHCP leases and/or an ARP table

To run all features appropriate for a given context, use component groups:

* `--run-daemon`
    * implies `--resize-disk` 
    * example usage: [`tart-guest-daemon.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-daemon.plist)
* `--run-agent`
    * implies `--run-vdagent --run-rpc` 
    * example usage: [`tart-guest-agent.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-agent.plist)
