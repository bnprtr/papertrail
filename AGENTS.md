# Papertrail Agent Rules

This file defines the contract for contributors and AI coding agents working on Papertrail.

## Priority Directive: Changelog Discipline

Any change that affects **user-visible behavior** MUST be recorded via changelog fragments.

### When to add a fragment
Add a fragment under `changelog.d/` for:
- CLI flags / output changes
- GitHub Action behavior / inputs / outputs
- Logic changes in release automation

Docs-only changes can skip fragments.

### PR Hygiene
- Use PR titles in the form: `<type>(<scope>): <title>` (scope optional).
- Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`.
- Fragments are required for non-doc changes unless the `no-changelog` label is applied.

## Core Principles

### Determinism
- The tool must produce the exact same `CHANGELOG.md` and release notes given the same set of fragments and configuration.
- Never depend on map iteration order.
- Fragment ordering must be: component order (from config), then type order (from config), then filename ascending.

### Fail Loudly
- Unsupported configuration or invalid fragment schema must produce clear, structured errors.
- Do not silently ignore errors in the release path.

### Additive Evolution
- Never change the semantics of existing configuration keys without a version bump or clear migration path.

## Code Style

- **Explicitness**: Prefer straightforward control flow and descriptive names.
- **Small Dependencies**: Keep the core CLI dependency tree as small as possible (currently only `gopkg.in/yaml.v3`).
- **Typed Errors**: Include context in errors (e.g., path to the invalid fragment).

## Testing

- **Golden Tests**: Use deterministic matching for changelog generation.
- **Unit Tests**: Test semver bumping logic, PR title parsing, and fragment validation.

## Release Process

Papertrail uses itself to manage its own releases.
1. Land fragments on `main`.
2. A draft release PR is automatically opened.
3. Merging the release PR triggers the `publish` workflow, which tags the release and builds binaries.

## Agent prompt template (optional)

If you’re using an AI coding agent, give it a concrete reminder to keep changelog hygiene consistent:

```text
Any user-visible change must include a changelog fragment under changelog.d/.
Use .papertrail.config.yml for valid components and fragment types.
Keep fragment summaries user-outcome focused.
```

