# Papertrail

Papertrail is a generic, fragment-first changelog and release management tool. It avoids merge conflicts and stale "Unreleased" sections by using small YAML fragments for each change, which are merged into a canonical `CHANGELOG.md` only at release time.

## Key Features

- **Fragment-driven**: Changes are recorded in `changelog.d/*.yml` files.
- **Deterministic**: Changelog ordering is stable and configurable via `.papertrail.config.yml`.
- **Automated Releases**: Supports automated version bumping, release notes generation, and draft release PRs.
- **CI Integration**: Built-in support for PR title validation, fragment requirement checks, and PR preview comments.
- **Multi-distribution**: Available as a CLI (Go install/run or prebuilt binaries) and as GitHub Actions.

## Installation

### Go Install
```bash
go install github.com/bnprtr/papertrail/cmd/papertrail@latest
```

### Download Binaries
Download the latest binary for your platform from the [Releases](https://github.com/bnprtr/papertrail/releases) page.

## Usage

### 1. Initialize
Create a `changelog.d/` directory and a `.papertrail.config.yml` at your repo root.

### 2. Add a Fragment
When making a change, add a fragment in `changelog.d/`:
```yaml
component: CLI
type: new feature
summary: Added the `version` command to check current version.
```

### 3. CI Gating
Use Papertrail in your CI to ensure every PR has a fragment:
```yaml
- uses: bnprtr/papertrail/.github/actions/require-fragment@v0.1.0
  with:
    base-ref: main
```

### 4. Release
Run the merge command to update your `CHANGELOG.md` and archive fragments:
```bash
papertrail merge --version v1.0.0
```

## Agent-friendly workflow

Papertrail is designed to make it easy for humans and coding agents to collaborate without changelog merge conflicts:

- You add one small fragment per user-visible change under `changelog.d/`.
- Releases merge fragments into `CHANGELOG.md` and archive them under `changelog.d/archived/<version>/`.

### Example agent prompt

You can use a prompt like this in your repo to enforce good changelog hygiene across many sessions:

```text
You are a coding agent working in this repo.

Rules:
- Any user-visible change must include a changelog fragment under changelog.d/.
- Use the repo's .papertrail.config.yml for valid components and fragment types.
- Keep fragments small and user-outcome focused (no internal implementation details unless user-visible).

Task:
<describe the change>

When done:
- Add exactly one fragment unless the change spans multiple components.
```

## Configuration

Papertrail is configured via `.papertrail.config.yml`. You can define:
- **Versioning rules**: How different fragment types (e.g., `BREAKING CHANGE`) affect the SemVer bump.
- **Changelog ordering**: The order of component headings in the generated changelog.
- **PR policy**: Rules for PR title validation and fragment opt-out labeling.

See [.papertrail.config.yml](./.papertrail.config.yml) for an example.

## GitHub Actions

Papertrail provides several composite actions for easy integration:
- `bnprtr/papertrail/.github/actions/pr-title`: Validates PR titles.
- `bnprtr/papertrail/.github/actions/require-fragment`: Ensures fragments are present.
- `bnprtr/papertrail/.github/actions/preview`: Generates a preview comment in PRs.

### Example workflows (copy/paste)

PR title validation:

```yaml
name: pr-title
on:
  pull_request:
    types: [opened, edited, reopened, synchronize]
jobs:
  pr-title:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - uses: bnprtr/papertrail/.github/actions/pr-title@v0.1.0
```

Require a fragment on non-doc changes:

```yaml
name: changelog-fragment
on:
  pull_request:
    types: [opened, synchronize, reopened, edited, labeled, unlabeled]
    paths-ignore:
      - "docs/**"
      - "README.md"
      - "AGENTS.md"
      - "CHANGELOG.md"
jobs:
  require-fragment:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - uses: bnprtr/papertrail/.github/actions/require-fragment@v0.1.0
        with:
          base-ref: ${{ github.base_ref }}
```

Preview comment:

```yaml
name: changelog-preview
on:
  pull_request:
    types: [opened, synchronize, reopened, edited]
permissions:
  contents: read
  pull-requests: write
jobs:
  preview:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - uses: bnprtr/papertrail/.github/actions/preview@v0.1.0
        id: pt
        with:
          base-ref: ${{ github.base_ref }}
      - name: Find existing preview comment
        if: steps.pt.outputs.has_fragments == 'true'
        id: find
        uses: peter-evans/find-comment@v3
        with:
          issue-number: ${{ github.event.pull_request.number }}
          body-includes: "<!-- papertrail-preview -->"
      - name: Create or update preview comment
        if: steps.pt.outputs.has_fragments == 'true'
        uses: peter-evans/create-or-update-comment@v4
        with:
          issue-number: ${{ github.event.pull_request.number }}
          comment-id: ${{ steps.find.outputs.comment-id }}
          edit-mode: replace
          body-path: .changelog-preview.md
```

## Release automation (dogfooded here)

This repository includes (and uses) a full release pipeline under `.github/workflows/`:

- `changelog-fragment.yml`: Require a fragment on non-doc PRs (via workflow-level `paths-ignore`).
- `changelog-preview.yml`: Comment a changelog preview on PRs with fragments.
- `release-draft.yml`: Auto-maintain a **draft release PR** whenever fragments exist on `main`.
- `publish.yml`: On merge of the release PR, tag the release and publish a GitHub Release with attached binaries.
- `release.yml`: Optional manual workflow to open a release PR for an explicit version/bump.

## License
MIT

