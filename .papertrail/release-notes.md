## v1.0.0

### CLI

- **breaking**: Simplify and generalize configuration by making fragment types configurable, flattening version bump rules, and renaming the default config file to .papertrail.config.yml.
- **refactor**: Remove built-in PR title validation; Papertrail focuses on fragment-driven changelog and release automation.

### GitHub Actions

- **fix**: Fix base-ref handling in the preview and fragment-check composite actions by treating base-ref as a branch name (e.g. main) to avoid origin/origin prefixes.
- **patch**: Move release artifacts into a dedicated .papertrail/ directory to reduce repo root bloat.

