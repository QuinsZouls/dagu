# Fork notes — all features, no Pro license

This repository is a fork of `dagucloud/dagu` that removes the proprietary
"Pro license" enforcement entirely. RBAC/user management, audit logging, SSO
(OIDC and trusted-proxy sign-in), incident management (providers, policies,
notification monitor) and API keys without an artificial cap are always
available. There is no license key, no activation step, no phone-home to the
vendor console, no `dagu license` CLI subcommand, and no `/api/v1/license/*`
endpoints.

This document records what was removed, why it was removed *that* way, which
limitations remain, and what to expect when rebasing onto upstream.

## What was removed, and why real deletion

The removal is a **real deletion, not a stub**. The gates, the checker/manager
types, the `internal/license` package, the CLI command, the API endpoints, the
config surface, the UI paywalls and the e2e/CI/chart plumbing are gone — no
always-true `Checker`, no pass-through wrapper, no "licensed" naming that
always returns `true`. A stub would keep thousands of lines of live-looking
dead code around and would keep the *concept* of a limited build alive, which
is exactly the confusion this fork exists to end. After the removal there is
one edition of Dagu; `config.Version` is the only build identity.

**Honest limitation (read this before rebasing).** Deleting the old symbols
makes *references to those symbols* fail to compile — and nothing more. A
future upstream rebase that introduces a **new** gate with its **own new
symbols** (a fresh package, a fresh `if !entitled() { return 403 }`) compiles
cleanly and would silently re-gate features. Discipline cannot catch that.
The structural protection is the guard test in `internal/guard`, which scans
the tree for reintroduction tokens (license package paths, gate symbols,
`DAGU_LICENSE*`, console/activation URLs), asserts that `config.Config` and
`config.Definition` have no `License` field, and asserts that the embedded
OpenAPI spec contains no `/license/` paths. Keep that test green and extend
its token list when a new upstream gate appears.

## Config and environment compatibility (no deprecation shim)

The main config file is decoded by the non-strict viper/mapstructure path
(`internal/cmn/config/loader.go`, the plain `Unmarshal` around line 250).
Unknown keys were **always** ignored there: a YAML file containing a stale
`license:` block loads without error and without warning, before and after
this change. Because nothing ever broke, no deprecation shim, no pre-stripping
and no startup warning were added — a shim would only make the deleted section
look meaningful again. (Note the asymmetry: the editor-facing JSON schema
flags a stale `license:` block, while the runtime loader ignores it. Neither
is a crash.)

The environment variables `DAGU_LICENSE`, `DAGU_LICENSE_KEY`,
`DAGU_LICENSE_FILE`, `DAGU_LICENSE_COMMUNITY_FEATURES` and
`DAGU_LICENSE_CLOUD_URL` are now **inert**: they are not read, not mapped and
not warned about. Setting them changes nothing.

## On-disk license state is deliberately untouched

`$DAGU_HOME/data/license/` (activation files and the persistence collection)
is **not** migrated, **not** deleted and **not** warned about. Deleting a
user's files as a side effect of an upgrade would be worse than an orphan
directory, and leaving it in place means a downgrade to a licensed build still
finds its activation state. This is a deliberate non-action.

## What was deliberately NOT changed

License *policy* is untouched: `LICENSE`, `ui/LICENSE.md`, `LICENSING.md`,
every `SPDX-License-Identifier` header, `.goreleaser.yaml`, the
`artifacthub.io/license: GPL-3.0-or-later` chart annotation, the
`charts/dagu/LICENSE*` files and the OpenAPI `info.license` block all remain
exactly as upstream wrote them. The fork stays GPL-3.0-or-later; see
[LICENSING.md](./LICENSING.md).

## Regenerating the API artifacts

The OpenAPI contract and its generated artifacts must be regenerated with the
real tools after any spec change — never hand-edited:

```sh
# from the repository root (installs the pinned codegen tools if missing)
make api

# from ui/ — regenerates the TypeScript client types
pnpm exec openapi-typescript --enum ../api/v1/api.yaml -o src/api/v1/schema.ts
```

`api/v1/api.gen.go` and `ui/src/api/v1/schema.ts` are generated; run the
matching command a second time and confirm `git diff --exit-code` stays clean
to prove the committed artifacts are true generator output.

## Expected rebase conflict sites

Rebasing onto `dagucloud/dagu` will conflict wherever upstream touches these
files; resolve by re-applying the removal (or regenerating), never by
hand-merging generated code:

- `api/v1/api.gen.go` — fully regenerated, ~49 k lines; conflict magnet forever
- `api/v1/api.yaml` — license paths/schema and license-flavored response text
- `internal/service/frontend/server.go` and
  `internal/service/frontend/templates.go`
- `internal/cmn/config/*` — config definition, loader, schema
- `internal/service/incident/service.go`
- `ui/src/App.tsx` — route wrappers and license status sync
