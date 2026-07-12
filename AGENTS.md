# Cloudflared OPNsense contributor guidance

Treat code, workflow configuration, and tests as the source of truth for
current behavior. Keep README.md focused on operator and contributor workflows,
and keep exact implementation details in the source that owns them.

## Verify changes

The repository has two Go modules. Use BSD Make for root targets and GNU Make
for `builder/` targets.

```sh
actionlint .github/workflows/*.yml
bmake lint
go test -race -count=1 ./...
make -C builder check
```

Run the relevant subset while iterating and the full set before submitting a
change that affects workflows, packaging, runtime behavior, or documentation.

## Preserve workflow boundaries

The build workflow owns package creation and publishing. Pull requests may run
the R2 preview round-trip, but they must not publish production objects or
create a release. Keep caller permissions sufficient for every nested reusable
workflow action, including attestations.

## Prove UI changes

Use the shared [OPNsense UI testing guide](https://github.com/agoodkind/configs/blob/main/docs/opnsense/ui-testing.md)
when a change needs browser proof on a real router. Do not hardcode testbed
hosts, addresses, or ports here. Derive forwarding details from the current
`configs` inventory and access documentation, then test
`/ui/cloudflared/settings`.

Pair a screenshot of the forwarded page with the package install, service
restart, and tunnel validation appropriate to the changed behavior.
