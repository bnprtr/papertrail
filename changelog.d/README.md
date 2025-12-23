# Changelog fragments

This repo uses **changelog fragments** to avoid merge conflicts and “stale Unreleased sections” when multiple agents/sessions are active.

## When to add a fragment

Add a fragment for any change that affects **user-visible behavior**, including:

- CLI flags / output
- GitHub Action behavior / inputs / outputs
- Release automation logic

Docs-only changes can skip fragments.

## SemVer guidance

Releases use **Semantic Versioning** (`vMAJOR.MINOR.PATCH`).

- **PATCH**: bugfixes and patches that do not change public contracts.
- **MINOR**: additive features that are backwards compatible.
- **MAJOR**: breaking changes (changed semantics of existing flags/actions, removed fields).

### Version bump policy

Papertrail’s version bump behavior is configured in `.papertrail.config.yml` under `versioning.rules`.

## Fragment format

Each fragment is a small YAML file placed directly under `changelog.d/`.

- file name: any unique name ending with `.yml` or `.yaml` (recommend: `YYYYMMDD_<short_slug>.yml`)
- required fields: `component`, `type`, `summary`
- optional fields: `refs`

Example:

```yaml
component: CLI
type: bugfix
summary: Fix incorrect CLI output.
refs:
  - cmd/papertrail/main.go
```

## Merging fragments

Fragments are merged into `CHANGELOG.md` at release time by the `papertrail` tool.

After merge, fragments are archived under `changelog.d/archived/<version>/`.

## Pending release behavior

When `main` contains unarchived fragments under `changelog.d/`, the repo has a **pending release**.

- A draft release PR is automatically opened/updated and includes:
  - updated `CHANGELOG.md`
  - archived fragments under `changelog.d/archived/<version>/`
  - `.papertrail/release-notes.md` used as the GitHub Release body
  - `.papertrail/release-version` used by the publish workflow

## Best practices

- Prefer **one fragment per PR**. Split only when a single PR genuinely spans multiple components or change types.
- Keep summaries **user-outcome focused**.
- **Opt-out label**: if a PR is truly non-user-visible, add the PR label `no-changelog` to opt out of fragments.
 

