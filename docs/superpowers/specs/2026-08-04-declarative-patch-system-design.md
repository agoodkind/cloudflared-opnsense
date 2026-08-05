# Declarative cloudflared Patch System Design

The builder applies an ordered, version-aware patch manifest to its temporary
cloudflared checkout before compilation. Patch declarations live in TOML and
use either a committed unified-diff file or one verified upstream Git commit.

Every remote declaration names a ref and its expected full commit SHA. The
builder may follow a mutable pull-request or branch ref, but it stops if that
ref moves until the new commit is reviewed and recorded. One remote entry
represents one non-merge commit. Multi-commit changes use ordered entries.

All sources become unified diffs and share one application path. The builder
first checks whether a patch applies. If it does, the builder applies it. If
the forward check fails, an exact reverse check may prove the patch is already
upstream and permit a skip. Every other mismatch fails the build with both
check errors.

The first manifest migrates the FreeBSD diagnostic workaround to a committed
patch based on the FreeBSD Ports implementation. It also represents
cloudflare/cloudflared pull request 1707 as a guarded remote entry. OPNsense
runtime scripts and tunnel configuration do not change.
