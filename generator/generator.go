package generator

import (
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/personal-stack/jira-cli/internal/logging"
	"github.com/personal-stack/jira-cli/internal/operation"
)

// Result is the full set of generated files, keyed by slash-separated path
// relative to the repository root.
type Result struct {
	Files    map[string][]byte
	Profiles []*operation.Profile
}

// Options control generation.
type Options struct {
	Root     string // repository root
	Manifest string // manifest path relative to Root
	OutDir   string // output directory relative to Root (default "generated")
	Log      *slog.Logger
}

// Generate runs the full pipeline for every profile in the manifest.
// It is pure with respect to its inputs: identical inputs yield identical
// bytes (no timestamps, map iteration is always sorted).
func Generate(opts Options) (*Result, error) {
	if opts.OutDir == "" {
		opts.OutDir = "generated"
	}
	if opts.Log == nil {
		opts.Log = logging.Discard()
	}
	m, err := LoadManifest(path.Join(opts.Root, opts.Manifest))
	if err != nil {
		return nil, err
	}
	res := &Result{Files: map[string][]byte{}}
	for _, ps := range m.Profiles {
		log := opts.Log.With("profile", ps.Name)
		start := time.Now()
		prof, err := buildProfile(opts.Root, ps, log)
		if err != nil {
			log.Error("profile generation failed", "error", err)
			return nil, fmt.Errorf("profile %s: %w", ps.Name, err)
		}
		log.Info("profile generated", "commands", len(prof.Operations), "skipped", len(prof.Skipped),
			"warnings", len(prof.Warnings), "duration_ms", time.Since(start).Milliseconds())
		res.Profiles = append(res.Profiles, prof)
		pkg := PackageName(ps.Name)
		files, err := emitProfile(pkg, prof)
		if err != nil {
			return nil, fmt.Errorf("profile %s: emit: %w", ps.Name, err)
		}
		for name, b := range files {
			res.Files[path.Join(opts.OutDir, pkg, name)] = b
		}
	}
	reg, err := emitRegistry(res.Profiles)
	if err != nil {
		return nil, err
	}
	res.Files[path.Join(opts.OutDir, "registry.go")] = reg
	return res, nil
}

func buildProfile(root string, ps ProfileSpec, log *slog.Logger) (*operation.Profile, error) {
	prof := &operation.Profile{Name: ps.Name, Deployment: ps.Deployment, BasePath: strings.TrimRight(ps.BasePath, "/")}
	if prof.BasePath != "" && !strings.HasPrefix(prof.BasePath, "/") {
		return nil, fmt.Errorf("base_path must start with /")
	}

	// 1. Load specifications (checksum-pinned).
	var specs []*loadedSpec
	for _, ref := range ps.Specs {
		b, sum, err := readPinned(root, ref.Path, ref.SHA256)
		if err != nil {
			return nil, err
		}
		doc, err := loadSpec(b)
		if err != nil {
			return nil, fmt.Errorf("spec %s: %w", ref.Name, err)
		}
		if doc.Info.Version != ref.Version {
			return nil, fmt.Errorf("spec %s: manifest version %q != info.version %q; run `jiragen update`", ref.Name, ref.Version, doc.Info.Version)
		}
		specs = append(specs, &loadedSpec{ref: ref, sha256: sum, doc: doc})
		log.Info("spec loaded", "spec", ref.Name, "source", ref.Source, "path", ref.Path, "version", doc.Info.Version, "sha256", sum, "paths", doc.Paths.Len())
		src := operation.Source{Name: ref.Name, Kind: ref.Source, Version: doc.Info.Version, Path: ref.Path, SHA256: sum}
		if len(specs) == 1 {
			prof.Spec = src
		} else {
			prof.Overlays = append(prof.Overlays, src)
		}
	}

	// 2. Load overlays and risk metadata (checksum-pinned).
	var overlays []*Overlay
	var names []string
	for _, f := range ps.Overlays {
		b, sum, err := readPinned(root, f.Path, f.SHA256)
		if err != nil {
			return nil, err
		}
		o, err := parseOverlay(b, f.Path)
		if err != nil {
			return nil, err
		}
		overlays = append(overlays, o)
		names = append(names, f.Path)
		log.Info("overlay loaded", "path", f.Path, "sha256", sum, "rename_paths", len(o.RenamePaths), "exclusions", len(o.Exclude), "operations", len(o.Operations), "schema_aliases", len(o.SchemaAliases))
		prof.Overlays = append(prof.Overlays, operation.Source{Name: path.Base(f.Path), Kind: "overlay", Path: f.Path, SHA256: sum})
	}
	var rf *RiskFile
	if ps.Risk != nil {
		b, sum, err := readPinned(root, ps.Risk.Path, ps.Risk.SHA256)
		if err != nil {
			return nil, err
		}
		if rf, err = parseRisk(b, ps.Risk.Path); err != nil {
			return nil, err
		}
		log.Info("risk metadata loaded", "path", ps.Risk.Path, "sha256", sum, "rules", len(rf.Operations), "admin_tags", len(rf.AdminTags))
		prof.Overlays = append(prof.Overlays, operation.Source{Name: path.Base(ps.Risk.Path), Kind: "risk", Path: ps.Risk.Path, SHA256: sum})
	}

	// 3. Collect operations, apply overlays, then validate the corrected docs.
	if err := renamePaths(specs, overlays, names); err != nil {
		return nil, err
	}
	var ops []*rawOp
	for _, s := range specs {
		ops = append(ops, collectOps(s)...)
	}
	collected := len(ops)
	ops, skips, err := applyOverlays(ops, overlays, names)
	if err != nil {
		return nil, err
	}
	log.Debug("overlays applied", "collected", collected, "remaining", len(ops), "excluded", len(skips))
	for _, s := range specs {
		if err := validateDoc(s); err != nil {
			return nil, fmt.Errorf("spec %s failed validation after overlays: %w", s.ref.Name, err)
		}
	}

	// 4. Detect conflicts between specifications: never merge silently.
	var errs []error
	byKey := map[string]*rawOp{}
	for _, r := range ops {
		if prev, ok := byKey[r.key()]; ok {
			errs = append(errs, fmt.Errorf("%s is defined by both %s and %s; exclude one in an overlay", r.key(), prev.spec.ref.Name, r.spec.ref.Name))
			continue
		}
		byKey[r.key()] = r
	}

	// 5. Map operations.
	aliases := map[string]string{}
	for _, o := range overlays {
		for k, v := range o.SchemaAliases {
			aliases[k] = v
		}
	}
	for _, r := range ops {
		op, err := mapOperation(r, aliases)
		var u errUnsupported
		switch {
		case errors.As(err, &u):
			skips = append(skips, skipped{r, u.msg})
			log.Debug("operation skipped", "operation_id", r.origID, "method", r.method, "path", r.path, "reason", u.msg)
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("%s (%s): %w", r.key(), r.op.OperationID, err))
			continue
		}
		op.Path = r.path
		op.Group = kebab(op.Tag)
		if r.ovr != nil && r.ovr.Group != "" {
			op.Group = r.ovr.Group
		}
		prof.Operations = append(prof.Operations, *op)
	}
	ovrByKey := map[string]*OpOverride{}
	for _, r := range ops {
		if r.ovr != nil {
			ovrByKey[r.key()] = r.ovr
		}
	}

	// 6. Unique operationIds (after overlay renames).
	ids := map[string][]string{}
	for _, op := range prof.Operations {
		ids[op.OperationID] = append(ids[op.OperationID], op.Method+" "+op.Path)
	}
	for _, id := range sortedKeys(ids) {
		if len(ids[id]) > 1 {
			errs = append(errs, fmt.Errorf("duplicate operationId %q: %s; rename via overlay operation_id", id, strings.Join(ids[id], ", ")))
		}
	}

	// 7. Command names and collisions.
	var warns []string
	errs = append(errs, assignNames(prof.Operations, ovrByKey, &warns)...)
	badGroup := map[string]bool{}
	for i := range prof.Operations {
		op := &prof.Operations[i]
		if reservedCommands[op.Group] && !badGroup[op.Group] {
			badGroup[op.Group] = true
			errs = append(errs, fmt.Errorf("group %q (tag %q) collides with a built-in command; set group via overlay", op.Group, op.Tag))
		}
		seen := map[string]bool{}
		for _, p := range op.Params {
			if p.In == operation.InPath {
				continue
			}
			if reservedFlags[p.Flag] {
				errs = append(errs, fmt.Errorf("%s %s: flag --%s (param %s) collides with a built-in flag; set flag via overlay", op.Group, op.Name, p.Flag, p.Name))
			}
			if seen[p.Flag] {
				errs = append(errs, fmt.Errorf("%s %s: duplicate flag --%s", op.Group, op.Name, p.Flag))
			}
			seen[p.Flag] = true
		}
	}

	// 8. Risk classification; stale risk entries are errors.
	used := map[string]bool{}
	for i := range prof.Operations {
		classify(&prof.Operations[i], rf)
		used[prof.Operations[i].OperationID] = true
	}
	if rf != nil {
		for _, id := range sortedKeys(rf.Operations) {
			if !used[id] {
				errs = append(errs, fmt.Errorf("risk file: operation %q does not exist (stale entry?)", id))
			}
		}
	}
	if len(errs) > 0 {
		log.Error("generation errors", "count", len(errs))
		return nil, errors.Join(errs...)
	}

	sort.SliceStable(prof.Operations, func(i, j int) bool {
		a, b := prof.Operations[i], prof.Operations[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Name < b.Name
	})
	for _, s := range skips {
		id := s.r.origID
		prof.Skipped = append(prof.Skipped, operation.Skipped{OperationID: id, Method: s.r.method, Path: s.r.path, Reason: s.reason})
	}
	sort.SliceStable(prof.Skipped, func(i, j int) bool {
		a, b := prof.Skipped[i], prof.Skipped[j]
		return a.Path+a.Method < b.Path+b.Method
	})
	sort.Strings(warns)
	prof.Warnings = warns
	return prof, nil
}

// assignNames gives every operation a command name, unique within its group.
// Rule: overlay command > short name (tag noun removed) > full operationId.
// If the short names of several operations collide, all of them fall back to
// their full names; if those still collide, generation fails.
func assignNames(ops []operation.Operation, ovr map[string]*OpOverride, warns *[]string) []error {
	type idx struct{ group, name string }
	explicit := map[int]bool{}
	for i := range ops {
		op := &ops[i]
		if o := ovr[op.Method+" "+op.Path]; o != nil && o.Command != "" {
			op.Name = o.Command
			explicit[i] = true
		} else {
			op.Name = commandName(op.OperationID, op.Tag)
		}
	}
	count := func() map[idx][]int {
		c := map[idx][]int{}
		for i := range ops {
			k := idx{ops[i].Group, ops[i].Name}
			c[k] = append(c[k], i)
		}
		return c
	}
	for _, list := range count() {
		if len(list) < 2 {
			continue
		}
		for _, i := range list {
			if !explicit[i] {
				ops[i].Name = fullName(ops[i].OperationID)
			}
		}
	}
	var errs []error
	c := count()
	keys := make([]idx, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].group+" "+keys[i].name < keys[j].group+" "+keys[j].name })
	for _, k := range keys {
		if list := c[k]; len(list) > 1 {
			var who []string
			for _, i := range list {
				who = append(who, ops[i].Method+" "+ops[i].Path+" ("+ops[i].OperationID+")")
			}
			errs = append(errs, fmt.Errorf("command %q %q is ambiguous: %s; set command via overlay", k.group, k.name, strings.Join(who, ", ")))
		}
	}
	for i := range ops {
		if !explicit[i] && numericSuffixRe.MatchString(ops[i].OperationID) {
			*warns = append(*warns, fmt.Sprintf("%s %s: name derived from suffixed operationId %s; consider an overlay command name", ops[i].Group, ops[i].Name, ops[i].OperationID))
		}
	}
	return errs
}
