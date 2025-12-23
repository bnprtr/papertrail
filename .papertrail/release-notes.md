## v0.0.1

### CLI

- **breaking**: Simplify and generalize configuration by making fragment types configurable, flattening version bump rules, and renaming the default config file to .papertrail.config.yml.
- **patch**: Remove repo-specific default component/type ordering; when ordering is not configured, Papertrail uses deterministic lexicographic ordering and accepts any fragment type.
- **patch**: Remove hardcoded bump semantics for specific fragment type names; when no bump rule matches, Papertrail defaults to patch and expects explicit config mapping.
- **patch**: Use calmer default fragment type names (breaking, feature, fix, patch, refactor, docs) while keeping compatibility via type aliases.
- **refactor**: Remove built-in PR title validation; Papertrail focuses on fragment-driven changelog and release automation.

### GitHub Actions

- **fix**: Fix base-ref handling in the preview and fragment-check composite actions by treating base-ref as a branch name (e.g. main) to avoid origin/origin prefixes.
- **patch**: Move release artifacts into a dedicated .papertrail/ directory to reduce repo root bloat.

