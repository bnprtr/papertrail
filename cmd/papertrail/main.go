package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type fragment struct {
	Component string   `yaml:"component"`
	Type      string   `yaml:"type"`
	Summary   string   `yaml:"summary"`
	Refs      []string `yaml:"refs,omitempty"`
}

type item struct {
	Path string
	Frag fragment
}

type releaseManifest struct {
	Versioning struct {
		Rules map[string]string `yaml:"rules"`
	} `yaml:"versioning"`

	Changelog struct {
		// Components defines the preferred order for component headings.
		// Unknown components are appended deterministically.
		Components []string `yaml:"components"`

		// ComponentsOrder is a legacy alias for Components (kept for backward compatibility).
		ComponentsOrder []string `yaml:"components_order"`

		StrictComponents bool `yaml:"strict_components"`
	} `yaml:"changelog"`

	Types struct {
		// Order defines the allowed fragment types and the preferred ordering in output.
		// Values are treated case-insensitively and normalized internally.
		Order []string `yaml:"order"`
		// Aliases maps alternate type spellings to canonical types.
		Aliases map[string]string `yaml:"aliases"`
	} `yaml:"types"`

	PRPolicy struct {
		TitleValidation struct {
			Enabled      bool              `yaml:"enabled"`
			AllowedTypes []string          `yaml:"allowed_types"`
			TypeAliases  map[string]string `yaml:"type_aliases"`
		} `yaml:"title_validation"`

		FragmentRequirement struct {
			OptOutLabel string `yaml:"opt_out_label"`
		} `yaml:"fragment_requirement"`
	} `yaml:"pr_policy"`
}

var (
	defaultComponentOrder = []string{
		"CLI",
		"GitHub Actions",
	}
	defaultTypeOrder = []string{
		"BREAKING CHANGE",
		"NEW FEATURE",
		"BUGFIX",
		"PATCH",
		"REFACTOR",
		"DOCS UPDATE",
	}
)

const (
	previewMarker = "<!-- papertrail-preview -->"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "check":
		if err := cmdCheck(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	case "bump":
		if err := cmdBump(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	case "pr-title":
		if err := cmdPRTitle(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	case "pr-fragment":
		if err := cmdPRFragment(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	case "preview":
		if err := cmdPreview(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	case "merge":
		if err := cmdMerge(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "papertrail: manage changelog fragments and releases")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  papertrail check --fragments <dir>")
	fmt.Fprintln(w, "  papertrail bump --base vX.Y.Z --fragments <dir> [--manifest <path>]")
	fmt.Fprintln(w, "  papertrail pr-title [--manifest <path>]   (reads GITHUB_EVENT_PATH)")
	fmt.Fprintln(w, "  papertrail pr-fragment --base-ref <ref> --fragments <dir> [--manifest <path>]   (reads GITHUB_EVENT_PATH)")
	fmt.Fprintln(w, "  papertrail preview <fragment.yml> [more fragments...]")
	fmt.Fprintln(w, "  papertrail merge --version vX.Y.Z --fragments <dir> --changelog <path> [--date YYYY-MM-DD] [--release-notes-out <path>]")
	fmt.Fprintln(w, "")
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	fragmentsDir := fs.String("fragments", "changelog.d", "fragments directory")
	manifestPath := fs.String("manifest", "", "optional release config YAML path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, _ := loadManifestDefault(*manifestPath)

	files, err := listFragmentFiles(*fragmentsDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no fragments found under %q", *fragmentsDir)
	}

	var allErrs []string
	for _, path := range files {
		_, err := readAndValidateFragment(path, manifest)
		if err != nil {
			allErrs = append(allErrs, fmt.Sprintf("%s: %s", path, err.Error()))
		}
	}
	if len(allErrs) > 0 {
		sort.Strings(allErrs)
		return errors.New(strings.Join(allErrs, "\n"))
	}
	return nil
}

func cmdBump(args []string) error {
	fs := flag.NewFlagSet("bump", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})

	base := fs.String("base", "", "base version like v1.2.3 (required)")
	fragmentsDir := fs.String("fragments", "changelog.d", "fragments directory")
	manifestPath := fs.String("manifest", "", "optional release config YAML path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" {
		return fmt.Errorf("--base is required (e.g. v0.1.0)")
	}
	if !isSemverV(*base) {
		return fmt.Errorf("invalid --base %q (expected vMAJOR.MINOR.PATCH)", *base)
	}

	manifest, err := loadManifestDefault(*manifestPath)
	if err != nil {
		return err
	}

	files, err := listFragmentFiles(*fragmentsDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no fragments found under %q", *fragmentsDir)
	}

	rules := manifest.Versioning.Rules

	var bump bumpKind = bumpPatch
	for _, path := range files {
		f, err := readAndValidateFragment(path, manifest)
		if err != nil {
			return fmt.Errorf("invalid fragment %s: %w", path, err)
		}
		bt, ok := bumpFromRules(rules, f.Type)
		if !ok {
			// No manifest: fall back to defaults.
			switch strings.ToUpper(strings.TrimSpace(f.Type)) {
			case "BREAKING CHANGE":
				bt = bumpMajor
			case "NEW FEATURE":
				bt = bumpMinor
			default:
				bt = bumpPatch
			}
		}
		if bt > bump {
			bump = bt
		}
		if bump == bumpMajor {
			break
		}
	}

	next, err := bumpSemver(*base, bump)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, next)
	return nil
}

func cmdPreview(args []string) error {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	manifestPath := fs.String("manifest", "", "optional release config YAML path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return fmt.Errorf("preview requires at least one fragment file path")
	}

	manifest, _ := loadManifestDefault(*manifestPath)

	items := make([]item, 0, len(files))
	for _, p := range files {
		f, err := readAndValidateFragment(p, manifest)
		if err != nil {
			return fmt.Errorf("invalid fragment %s: %w", p, err)
		}
		items = append(items, item{Path: p, Frag: f})
	}

	out := renderPreview(items, manifest)
	_, _ = os.Stdout.Write(out)
	return nil
}

func cmdPRTitle(args []string) error {
	fs := flag.NewFlagSet("pr-title", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	manifestPath := fs.String("manifest", "", "optional release config YAML path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := loadManifestDefault(*manifestPath)
	if err != nil {
		return err
	}
	cfg := prPolicyFromManifest(manifest)

	if !cfg.TitleEnabled {
		return nil
	}

	evPath := strings.TrimSpace(os.Getenv("GITHUB_EVENT_PATH"))
	if evPath == "" {
		return fmt.Errorf("GITHUB_EVENT_PATH is required")
	}
	title, _, err := readPRTitleAndLabels(evPath)
	if err != nil {
		return err
	}
	if err := validatePRTitle(cfg, title); err != nil {
		return err
	}
	return nil
}

func cmdPRFragment(args []string) error {
	fs := flag.NewFlagSet("pr-fragment", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	baseRef := fs.String("base-ref", "", "base ref to diff against (required), e.g. origin/main")
	fragmentsDir := fs.String("fragments", "changelog.d", "fragments directory")
	manifestPath := fs.String("manifest", "", "optional release config YAML path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*baseRef) == "" {
		return fmt.Errorf("--base-ref is required")
	}

	manifest, err := loadManifestDefault(*manifestPath)
	if err != nil {
		return err
	}
	cfg := prPolicyFromManifest(manifest)

	evPath := strings.TrimSpace(os.Getenv("GITHUB_EVENT_PATH"))
	if evPath == "" {
		return fmt.Errorf("GITHUB_EVENT_PATH is required")
	}
	_, labels, err := readPRTitleAndLabels(evPath)
	if err != nil {
		return err
	}

	changed, err := gitChangedFiles(*baseRef)
	if err != nil {
		return err
	}

	if cfg.OptOutLabel != "" && contains(labels, cfg.OptOutLabel) {
		return nil
	}

	// Fragment required: ensure at least one fragment file is part of the PR diff.
	var fragChanged bool
	for _, f := range changed {
		if strings.HasPrefix(f, *fragmentsDir+"/") && (strings.HasSuffix(f, ".yml") || strings.HasSuffix(f, ".yaml")) {
			fragChanged = true
			break
		}
	}
	if !fragChanged {
		msg := "Non-doc changes detected, but no changelog fragment found under " + *fragmentsDir + "/"
		if cfg.OptOutLabel != "" {
			msg += " (if truly non-user-visible, add label: " + cfg.OptOutLabel + ")"
		}
		return errors.New(msg)
	}

	// Validate all fragments in the repo (catches schema drift deterministically).
	return cmdCheck([]string{"--fragments", *fragmentsDir, "--manifest", *manifestPath})
}

func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})

	version := fs.String("version", "", "version like v1.2.3 (required)")
	date := fs.String("date", "", "release date YYYY-MM-DD (default: today UTC)")
	fragmentsDir := fs.String("fragments", "changelog.d", "fragments directory")
	changelogPath := fs.String("changelog", "CHANGELOG.md", "changelog path")
	archiveDir := fs.String("archive", "changelog.d/archived", "archive directory")
	releaseNotesOut := fs.String("release-notes-out", "", "write release notes body to this path")
	manifestPath := fs.String("manifest", "", "optional release config YAML path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *version == "" {
		return fmt.Errorf("--version is required (e.g. v0.1.0)")
	}
	if !strings.HasPrefix(*version, "v") || strings.Count(*version, ".") != 2 {
		return fmt.Errorf("invalid --version %q (expected vMAJOR.MINOR.PATCH)", *version)
	}

	releaseDate := *date
	if releaseDate == "" {
		releaseDate = time.Now().UTC().Format("2006-01-02")
	} else if !looksLikeDate(releaseDate) {
		return fmt.Errorf("invalid --date %q (expected YYYY-MM-DD)", releaseDate)
	}

	files, err := listFragmentFiles(*fragmentsDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no fragments found under %q", *fragmentsDir)
	}

	manifest, err := loadManifestDefault(*manifestPath)
	if err != nil {
		return err
	}

	items := make([]item, 0, len(files))
	for _, p := range files {
		f, err := readAndValidateFragment(p, manifest)
		if err != nil {
			return fmt.Errorf("invalid fragment %s: %w", p, err)
		}
		items = append(items, item{Path: p, Frag: f})
	}

	section, releaseNotes := renderReleaseSection(*version, releaseDate, items, manifest)

	orig, err := os.ReadFile(*changelogPath)
	if err != nil {
		return err
	}
	if bytes.Contains(orig, []byte("\n## "+*version+" (")) {
		return fmt.Errorf("CHANGELOG already contains a section for %s", *version)
	}
	updated, err := insertReleaseSection(orig, section)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*changelogPath, updated, 0644); err != nil {
		return err
	}

	if *releaseNotesOut != "" {
		if err := os.WriteFile(*releaseNotesOut, releaseNotes, 0644); err != nil {
			return err
		}
	}

	archivePath := filepath.Join(*archiveDir, *version)
	if err := os.MkdirAll(archivePath, 0755); err != nil {
		return err
	}
	for _, it := range items {
		dst := filepath.Join(archivePath, filepath.Base(it.Path))
		if err := os.Rename(it.Path, dst); err != nil {
			return err
		}
	}

	return nil
}

func listFragmentFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip archives.
			if path != dir && filepath.Base(path) == "archived" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yml" && ext != ".yaml" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func readAndValidateFragment(path string, manifest releaseManifest) (fragment, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return fragment{}, err
	}
	var f fragment
	if err := yaml.Unmarshal(b, &f); err != nil {
		return fragment{}, fmt.Errorf("invalid YAML: %w", err)
	}
	f.Component = strings.TrimSpace(f.Component)
	f.Type = strings.TrimSpace(strings.ToUpper(f.Type))
	f.Summary = strings.TrimSpace(f.Summary)
	for i := range f.Refs {
		f.Refs[i] = strings.TrimSpace(f.Refs[i])
	}

	if f.Component == "" {
		return fragment{}, fmt.Errorf("missing required field: component")
	}
	if f.Type == "" {
		return fragment{}, fmt.Errorf("missing required field: type")
	}
	if f.Summary == "" {
		return fragment{}, fmt.Errorf("missing required field: summary")
	}
	if manifest.Changelog.StrictComponents {
		order := componentOrderFromManifest(manifest)
		if !contains(order, f.Component) {
			return fragment{}, fmt.Errorf("unknown component %q (expected one of %s)", f.Component, strings.Join(order, ", "))
		}
	}
	f.Type = canonicalizeFragmentType(f.Type, manifest)
	order := typeOrderFromManifest(manifest)
	if !contains(order, f.Type) {
		return fragment{}, fmt.Errorf("unknown type %q (expected one of %s)", f.Type, strings.Join(order, ", "))
	}
	return f, nil
}

func renderReleaseSection(version, date string, items []item, manifest releaseManifest) (section []byte, releaseNotes []byte) {
	// Deterministic ordering: component order, then type order, then filename.
	type row struct {
		path string
		frag fragment
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		rows = append(rows, row{path: it.Path, frag: it.Frag})
	}

	sort.Slice(rows, func(i, j int) bool {
		ai := componentIndex(rows[i].frag.Component, manifest)
		aj := componentIndex(rows[j].frag.Component, manifest)
		if ai != aj {
			return ai < aj
		}
		ti := typeIndex(rows[i].frag.Type, manifest)
		tj := typeIndex(rows[j].frag.Type, manifest)
		if ti != tj {
			return ti < tj
		}
		return filepath.Base(rows[i].path) < filepath.Base(rows[j].path)
	})

	byComponent := map[string][]row{}
	for _, r := range rows {
		byComponent[r.frag.Component] = append(byComponent[r.frag.Component], r)
	}

	var buf bytes.Buffer
	var notes bytes.Buffer

	fmt.Fprintf(&buf, "## %s (%s)\n\n", version, date)
	fmt.Fprintf(&notes, "## %s\n\n", version)

	for _, comp := range orderedComponents(items, manifest) {
		rs := byComponent[comp]
		if len(rs) == 0 {
			continue
		}
		fmt.Fprintf(&buf, "### %s\n\n", comp)
		fmt.Fprintf(&notes, "### %s\n\n", comp)
		for _, r := range rs {
			fmt.Fprintf(&buf, "- **%s**: %s\n", displayType(r.frag.Type), ensurePeriod(r.frag.Summary))
			fmt.Fprintf(&notes, "- **%s**: %s\n", displayType(r.frag.Type), ensurePeriod(r.frag.Summary))
		}
		fmt.Fprintf(&buf, "\n")
		fmt.Fprintf(&notes, "\n")
	}

	return buf.Bytes(), notes.Bytes()
}

func renderPreview(items []item, manifest releaseManifest) []byte {
	// Deterministic ordering: component order, then type order, then filename.
	type row struct {
		path string
		frag fragment
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		rows = append(rows, row{path: it.Path, frag: it.Frag})
	}

	sort.Slice(rows, func(i, j int) bool {
		ai := componentIndex(rows[i].frag.Component, manifest)
		aj := componentIndex(rows[j].frag.Component, manifest)
		if ai != aj {
			return ai < aj
		}
		ti := typeIndex(rows[i].frag.Type, manifest)
		tj := typeIndex(rows[j].frag.Type, manifest)
		if ti != tj {
			return ti < tj
		}
		return filepath.Base(rows[i].path) < filepath.Base(rows[j].path)
	})

	byComponent := map[string][]row{}
	for _, r := range rows {
		byComponent[r.frag.Component] = append(byComponent[r.frag.Component], r)
	}

	var buf bytes.Buffer
	buf.WriteString(previewMarker + "\n")
	buf.WriteString("### Changelog preview\n\n")

	for _, comp := range orderedComponents(items, manifest) {
		rs := byComponent[comp]
		if len(rs) == 0 {
			continue
		}
		fmt.Fprintf(&buf, "#### %s\n\n", comp)
		for _, r := range rs {
			fmt.Fprintf(&buf, "- **%s**: %s\n", displayType(r.frag.Type), ensurePeriod(r.frag.Summary))
		}
		buf.WriteString("\n")
	}

	return buf.Bytes()
}

func displayType(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

func insertReleaseSection(changelog []byte, section []byte) ([]byte, error) {
	s := string(changelog)
	idx := findReleaseInsertionIndex(s)
	if idx < 0 || idx > len(s) {
		return nil, fmt.Errorf("could not find insertion point for release section in CHANGELOG")
	}

	head := s[:idx]
	tail := s[idx:]

	var out bytes.Buffer
	out.WriteString(head)
	if len(head) > 0 && !strings.HasSuffix(head, "\n\n") {
		if strings.HasSuffix(head, "\n") {
			out.WriteString("\n")
		} else {
			out.WriteString("\n\n")
		}
	}
	out.Write(section)
	out.WriteString(tail)
	return out.Bytes(), nil
}

func findReleaseInsertionIndex(changelog string) int {
	candidates := []int{
		strings.Index(changelog, "\n## 20"),
		strings.Index(changelog, "\n## v"),
	}
	best := -1
	for _, c := range candidates {
		if c < 0 {
			continue
		}
		if best < 0 || c < best {
			best = c
		}
	}
	if best < 0 {
		return len(changelog)
	}
	return best + 1
}

func ensurePeriod(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func componentIndex(c string, manifest releaseManifest) int {
	order := componentOrderFromManifest(manifest)
	for i, v := range order {
		if v == c {
			return i
		}
	}
	return len(order) + 1
}

func looksLikeDate(s string) bool {
	if len(s) != len("2006-01-02") {
		return false
	}
	if s[4] != '-' || s[7] != '-' {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

type bumpKind int

const (
	bumpPatch bumpKind = iota
	bumpMinor
	bumpMajor
)

func bumpSemver(base string, bump bumpKind) (string, error) {
	v := strings.TrimPrefix(base, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid semver %q", base)
	}
	ma, err := atoiStrict(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid semver %q", base)
	}
	mi, err := atoiStrict(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid semver %q", base)
	}
	pa, err := atoiStrict(parts[2])
	if err != nil {
		return "", fmt.Errorf("invalid semver %q", base)
	}

	switch bump {
	case bumpMajor:
		ma++
		mi = 0
		pa = 0
	case bumpMinor:
		mi++
		pa = 0
	case bumpPatch:
		pa++
	default:
		return "", fmt.Errorf("unknown bump kind")
	}
	return fmt.Sprintf("v%d.%d.%d", ma, mi, pa), nil
}

func atoiStrict(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func isSemverV(s string) bool {
	if !strings.HasPrefix(s, "v") {
		return false
	}
	v := strings.TrimPrefix(s, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := atoiStrict(p); err != nil {
			return false
		}
	}
	return true
}

func semverMajor(s string) int {
	if !strings.HasPrefix(s, "v") {
		return -1
	}
	v := strings.TrimPrefix(s, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return -1
	}
	ma, err := atoiStrict(parts[0])
	if err != nil {
		return -1
	}
	return ma
}

func validateBumpRules(rules map[string]string, path string) error {
	for k, v := range rules {
		vn := strings.ToLower(strings.TrimSpace(v))
		switch vn {
		case "major", "minor", "patch":
			// ok
		default:
			return fmt.Errorf("invalid %s[%q]=%q (expected major|minor|patch)", path, k, v)
		}
	}
	return nil
}

func bumpFromRules(rules map[string]string, fragmentType string) (bumpKind, bool) {
	if len(rules) == 0 {
		return bumpPatch, false
	}
	ft := strings.ToUpper(strings.TrimSpace(fragmentType))
	v, ok := rules[ft]
	if !ok {
		v, ok = rules["*"]
		if !ok {
			return bumpPatch, false
		}
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "major":
		return bumpMajor, true
	case "minor":
		return bumpMinor, true
	case "patch":
		return bumpPatch, true
	default:
		return bumpPatch, false
	}
}

func loadManifestDefault(path string) (releaseManifest, error) {
	mp := strings.TrimSpace(path)
	if mp == "" {
		for _, cand := range []string{".papertrail.config.yml", "papertrail.config.yml"} {
			if _, err := os.Stat(cand); err == nil {
				mp = cand
				break
			}
		}
	}
	if mp == "" {
		return releaseManifest{}, nil
	}
	b, err := os.ReadFile(mp)
	if err != nil {
		return releaseManifest{}, err
	}
	var manifest releaseManifest
	if err := yaml.Unmarshal(b, &manifest); err != nil {
		return releaseManifest{}, fmt.Errorf("invalid manifest YAML: %w", err)
	}
	if err := validateBumpRules(manifest.Versioning.Rules, "versioning.rules"); err != nil {
		return releaseManifest{}, err
	}
	manifest.Types.Aliases = normalizeTypeAliases(manifest.Types.Aliases)
	manifest.Types.Order = normalizeTypeOrder(manifest.Types.Order, manifest.Types.Aliases)
	manifest.Versioning.Rules = normalizeBumpRuleKeys(manifest.Versioning.Rules, manifest.Types.Aliases)
	return manifest, nil
}

func normalizeBumpRuleKeys(rules map[string]string, typeAliases map[string]string) map[string]string {
	if len(rules) == 0 {
		return rules
	}
	out := make(map[string]string, len(rules))
	for k, v := range rules {
		kk := strings.TrimSpace(k)
		if kk == "" {
			continue
		}
		if kk != "*" {
			kk = strings.ToUpper(kk)
			if canon, ok := typeAliases[kk]; ok {
				kk = canon
			}
		}
		out[kk] = v
	}
	return out
}

type prPolicy struct {
	TitleEnabled bool
	AllowedTypes []string
	TypeAliases  map[string]string
	OptOutLabel  string
}

func prPolicyFromManifest(m releaseManifest) prPolicy {
	p := prPolicy{
		TitleEnabled: m.PRPolicy.TitleValidation.Enabled,
		AllowedTypes: m.PRPolicy.TitleValidation.AllowedTypes,
		TypeAliases:  m.PRPolicy.TitleValidation.TypeAliases,
		OptOutLabel:  strings.TrimSpace(m.PRPolicy.FragmentRequirement.OptOutLabel),
	}
	if len(p.AllowedTypes) == 0 {
		p.AllowedTypes = []string{"feat", "fix", "docs", "chore", "refactor", "test"}
	}
	if p.TypeAliases == nil {
		p.TypeAliases = map[string]string{"feature": "feat", "bugfix": "fix"}
	}
	if p.OptOutLabel == "" {
		p.OptOutLabel = "no-changelog"
	}
	return p
}

func validatePRTitle(cfg prPolicy, title string) error {
	if !cfg.TitleEnabled {
		return nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("PR title is empty")
	}
	_, err := parsePRType(cfg, title)
	return err
}

func parsePRType(cfg prPolicy, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("PR title is empty")
	}
	colon := strings.Index(title, ":")
	if colon <= 0 {
		return "", fmt.Errorf("PR title must match: <type>(<scope>): <title> (scope optional)")
	}
	head := strings.TrimSpace(title[:colon])
	if head == "" {
		return "", fmt.Errorf("PR title must match: <type>(<scope>): <title> (scope optional)")
	}
	typ := head
	if i := strings.Index(head, "("); i >= 0 {
		typ = strings.TrimSpace(head[:i])
	}
	typ = strings.ToLower(typ)
	if alias, ok := cfg.TypeAliases[typ]; ok {
		typ = strings.ToLower(strings.TrimSpace(alias))
	}
	if !contains(cfg.AllowedTypes, typ) {
		return "", fmt.Errorf("invalid PR type %q; allowed types: %s", typ, strings.Join(cfg.AllowedTypes, ", "))
	}
	rest := strings.TrimSpace(title[colon+1:])
	if rest == "" {
		return "", fmt.Errorf("PR title must include a non-empty title after ':'")
	}
	return typ, nil
}

func typeIndex(t string, manifest releaseManifest) int {
	order := typeOrderFromManifest(manifest)
	for i, v := range order {
		if v == t {
			return i
		}
	}
	return len(order) + 1
}

func gitChangedFiles(baseRef string) ([]string, error) {
	out, err := runGit("diff", "--name-only", baseRef+"...HEAD")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

func readPRTitleAndLabels(eventPath string) (title string, labels []string, err error) {
	b, err := os.ReadFile(eventPath)
	if err != nil {
		return "", nil, err
	}
	var ev struct {
		PullRequest struct {
			Title  string `json:"title"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		return "", nil, fmt.Errorf("invalid GitHub event JSON: %w", err)
	}
	title = strings.TrimSpace(ev.PullRequest.Title)
	if title == "" {
		return "", nil, fmt.Errorf("could not read PR title from %s", eventPath)
	}
	for _, l := range ev.PullRequest.Labels {
		n := strings.TrimSpace(l.Name)
		if n != "" {
			labels = append(labels, n)
		}
	}
	sort.Strings(labels)
	out := labels[:0]
	var last string
	for _, n := range labels {
		if n == last {
			continue
		}
		out = append(out, n)
		last = n
	}
	return title, out, nil
}

func runGit(args ...string) (string, error) {
	return runCmd("git", args...)
}

func runCmd(bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", bin, strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func componentOrderFromManifest(manifest releaseManifest) []string {
	components := manifest.Changelog.Components
	if len(components) == 0 {
		components = manifest.Changelog.ComponentsOrder
	}
	if len(components) > 0 {
		seen := map[string]bool{}
		var out []string
		for _, c := range components {
			c = strings.TrimSpace(c)
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
		if len(out) > 0 {
			return out
		}
	}
	return defaultComponentOrder
}

func normalizeTypeAliases(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		kk := strings.ToUpper(strings.TrimSpace(k))
		vv := strings.ToUpper(strings.TrimSpace(v))
		if kk == "" || vv == "" {
			continue
		}
		out[kk] = vv
	}
	return out
}

func normalizeTypeOrder(order []string, aliases map[string]string) []string {
	if len(order) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range order {
		tt := strings.ToUpper(strings.TrimSpace(t))
		if tt == "" {
			continue
		}
		if canon, ok := aliases[tt]; ok {
			tt = canon
		}
		if seen[tt] {
			continue
		}
		seen[tt] = true
		out = append(out, tt)
	}
	return out
}

func typeOrderFromManifest(manifest releaseManifest) []string {
	if len(manifest.Types.Order) > 0 {
		return manifest.Types.Order
	}
	return defaultTypeOrder
}

func canonicalizeFragmentType(t string, manifest releaseManifest) string {
	tt := strings.ToUpper(strings.TrimSpace(t))
	if tt == "" {
		return tt
	}
	if canon, ok := manifest.Types.Aliases[tt]; ok {
		return canon
	}
	return tt
}

func orderedComponents(items []item, manifest releaseManifest) []string {
	known := componentOrderFromManifest(manifest)
	seenKnown := map[string]bool{}
	for _, c := range known {
		seenKnown[c] = true
	}

	present := map[string]bool{}
	for _, it := range items {
		present[it.Frag.Component] = true
	}

	var out []string
	for _, c := range known {
		if present[c] {
			out = append(out, c)
		}
	}

	var unknown []string
	for c := range present {
		if !seenKnown[c] {
			unknown = append(unknown, c)
		}
	}
	sort.Strings(unknown)
	out = append(out, unknown...)
	return out
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (n int, err error) { return len(p), nil }
