# Cloudflared OPNsense agent notes

## UI testing

Use the shared OPNsense UI testing guide in
[configs/docs/opnsense/ui-testing.md](https://github.com/agoodkind/configs/blob/main/docs/opnsense/ui-testing.md)
when a change needs browser proof from the real OPNsense UI.

Do not hardcode testbed hosts, addresses, or ports in this repository. Derive
the SSH forwarding inputs from the current `configs` inventory and access docs,
then set the Cloudflared settings path as the page under test:

```sh
REMOTE_PATH='/ui/cloudflared/settings'
```

Use a screenshot from the forwarded page as UI proof, and pair it with the
package install, service restart, and tunnel validation steps for the change
under test.
