# Guest agent for Tart VMs

A guest agent for Tart VMS is a lightweight background service that runs inside the virtual machine and enables enhanced communication between the host and guest and other useful features, such as automatic disk resizing.

Currently implemented features:

* Automatic disk resizing for macOS VMs with recovery partition removed (`--resize-disk`)
    * needs to be invoked as a launchd [global daemon](https://launchd.info/)
    * example usage: [`tart-guest-daemon.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-daemon.plist)
* Clipboard sharing for macOS VMs using our in-house SPICE vdagent implementation (`--run-vdagent`)
    * needs to be invoked as a launchd [global agent](https://launchd.info/)
    * example usage: [`tart-guest-agent.plist`](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-guest-agent.plist)
