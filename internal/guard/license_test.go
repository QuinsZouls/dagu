// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package guard holds structural regression guards for this fork: tests that
// pass only while an invariant promised by FORK.md actually holds.
//
// license_test.go implements ruling R5 of the license-removal plan: deleting
// the old symbols makes *references to them* fail to compile — and nothing
// more (R1a). A future upstream rebase can re-introduce license gating under
// brand-new symbols and compile cleanly, so this guard additionally checks
// the *concept* with a token scan plus two cheap structural assertions
// (config reflection, embedded OpenAPI spec).
package guard

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// scanTokens is the reintroduction vocabulary from ruling R5 of the removal
// plan. Exact, case-sensitive substrings — deliberately NOT the bare word
// "license" (it fires on every SPDX header, LICENSING.md, info.license, …).
// Extend this list when a new upstream gate appears under new vocabulary.
var scanTokens = []string{
	"internal/license",
	"HasActiveLicense",
	"IsFeatureEnabled",
	"FeatureRBAC",
	"FeatureAudit",
	"FeatureSSO",
	"DAGU_LICENSE",
	"license.community_features",
	"console.dagu.sh",
	"/license/status",
	"/license/activate",
	"/license/deactivate",
	"Pro license",
	"communityAPIKeyLimit",
}

// scanExtensions is the file scope from ruling R5.
var scanExtensions = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".yaml": true, ".yml": true,
	".json": true, ".md": true, ".sh": true, ".mjs": true,
}

// skipDirNames excludes version control and build-output directories (R5:
// .git, ui/node_modules, ui/dist and any build output dir). Note that
// "build" is intentionally NOT here — internal/build is a source package.
var skipDirNames = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	".local":       true,
}

// tokenHit is one match: file (slash-separated, relative to the module
// root), 1-based line, and the token that matched.
type tokenHit struct {
	File  string
	Line  int
	Token string
}

// guardException is a documented, deliberately NARROW exemption: exactly one
// file × one token. Never a whole directory, never a whole extension. Every
// entry must still match at least one real occurrence in the tree — the
// anti-rot assertion below fails the test on a stale exception, so this list
// cannot silently accrete.
type guardException struct {
	File   string
	Token  string
	Reason string
}

// guardExceptions carries the only tolerated hits in the tree:
//
//  1. The decision-D1 legacy-compat fixture — a deletion-proof test proving
//     that old operator configs (legacy `license:` YAML block +
//     DAGU_LICENSE* env vars) still Load() without error and are ignored.
//     It is compatibility *evidence*, not a live feature; it must keep
//     naming the legacy keys or it proves nothing.
//  2. FORK.md — the removal record the plan mandates (PLAN §407): it must
//     name what was removed (the package path, the feature name, the now-
//     inert env vars). Rewording it to dodge this scan would make the record
//     less honest, so it is excepted here and declares itself honestly
//     (R1a: "state this honestly").
var guardExceptions = []guardException{
	{
		File:   "internal/cmn/config/loader_test.go",
		Token:  "DAGU_LICENSE",
		Reason: "decision-D1 deletion-proof fixture TestLoad_LegacyLicenseKeysIgnored: legacy license env vars must be named to prove they still Load() silently ignored; not a live feature",
	},
	{
		File:   "FORK.md",
		Token:  "DAGU_LICENSE",
		Reason: "removal record must list the now-inert license env vars verbatim (PLAN §407 / decision D1); for humans, not gating",
	},
	{
		File:   "FORK.md",
		Token:  "internal/license",
		Reason: "removal record must name the package that was deleted (PLAN §407); for humans, not gating",
	},
	{
		File:   "FORK.md",
		Token:  "Pro license",
		Reason: "removal record names the removed feature by its real name; for humans, not gating",
	},
}

// guardSelf is this file: it contains the token list itself and must not
// scan itself. Matched by full relative path so other files with the same
// basename stay in scope.
const guardSelf = "internal/guard/license_test.go"

// TestLicenseTokensAbsent (leg 1) walks the module once and fails, listing
// file:line: token for EVERY hit, if any reintroduction token survives the
// exception list.
//
// Invoke this package as `go test ./internal/guard/ -count=1` (the form the
// plan's acceptance uses, and what any Makefile/CI target must keep).
// Go's test-result cache is keyed on the *build inputs* of the test binary,
// not on the working tree this test reads: reintroducing a token in
// README.md, charts/, ui/src or any other file that is not a Go import of
// this package changes nothing the compiler sees, so a cached run would
// replay a stale PASS without re-walking. -count=1 disables only the result
// cache (compilation stays cached), forcing a fresh scan every time for
// ~2 s. It is the correct invocation precisely because this test's oracle is
// external state, not its own inputs.
func TestLicenseTokensAbsent(t *testing.T) {
	start := time.Now()
	root := moduleRoot(t)

	toks := make([][]byte, len(scanTokens))
	for i, tok := range scanTokens {
		toks[i] = []byte(tok)
	}
	spdxTag := []byte("SPDX-License-Identifier")

	var hits []tokenHit
	// Standard filepath.WalkDir callback, deliberately ordered so pruning
	// happens before any file access: directory entries are checked first
	// and excluded directories return fs.SkipDir, which stops WalkDir from
	// descending — no child of a pruned directory is ever listed, stat-ed
	// or opened. Only after the dir check does a regular file get its
	// extension/exclusion checks, and os.ReadFile (the first content
	// touch) runs last, for extension-matching, non-excluded files only.
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// 1) Prune heavy directories before anything else.
		if d.IsDir() {
			if skipDirNames[d.Name()] {
				return fs.SkipDir // stop descent; children never enumerated
			}
			return nil
		}
		// 2) Skip symlinks/sockets/etc. without opening them.
		if !d.Type().IsRegular() {
			return nil
		}
		// 3) Cheap name-based filters before any file I/O.
		name := d.Name()
		if !scanExtensions[filepath.Ext(name)] {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == guardSelf || name == "LICENSING.md" ||
			strings.HasPrefix(name, "LICENSE") ||
			rel == "api/v1/oapi_20241018_mod.json" {
			return nil
		}
		// 4) First and only content read.
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// Cheap pre-filter: only split into lines if the file can contain
		// at least one token at all (no per-line work for the common case).
		anyToken := false
		for _, tok := range toks {
			if bytes.Contains(content, tok) {
				anyToken = true
				break
			}
		}
		if !anyToken {
			return nil
		}
		for i, line := range bytes.Split(content, []byte("\n")) {
			// PLAN line 49 trap: SPDX headers appear in every source file.
			if bytes.Contains(line, spdxTag) {
				continue
			}
			for _, tok := range toks {
				if bytes.Contains(line, tok) {
					hits = append(hits, tokenHit{File: rel, Line: i + 1, Token: string(tok)})
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking module root %s: %v", root, walkErr)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		if hits[i].Line != hits[j].Line {
			return hits[i].Line < hits[j].Line
		}
		return hits[i].Token < hits[j].Token
	})

	excepted := func(h tokenHit) bool {
		for _, ex := range guardExceptions {
			if h.File == ex.File && h.Token == ex.Token {
				return true
			}
		}
		return false
	}

	// Anti-rot: every exception must still match at least one real
	// occurrence. An exception whose evidence disappeared fails the test
	// and must be removed — exemptions cannot go stale in silence.
	for _, ex := range guardExceptions {
		found := false
		for _, h := range hits {
			if h.File == ex.File && h.Token == ex.Token {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stale guard exception: (%s, %q) matches no occurrence in the tree — delete the exception (%s)",
				ex.File, ex.Token, ex.Reason)
		}
	}

	var violations, kept []tokenHit
	for _, h := range hits {
		if excepted(h) {
			kept = append(kept, h)
		} else {
			violations = append(violations, h)
		}
	}
	// With -v this doubles as the pre-exception audit of the whole tree.
	for _, h := range kept {
		t.Logf("excepted: %s:%d: %s", h.File, h.Line, h.Token)
	}
	if len(violations) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "license-gating token(s) reintroduced: %d hit(s) (%d excepted):\n",
			len(violations), len(kept))
		for _, h := range violations {
			fmt.Fprintf(&b, "  %s:%d: %s\n", h.File, h.Line, h.Token)
		}
		b.WriteString("If this is a reintroduction of license gating, remove it. If it is\n")
		b.WriteString("documentation that must name the removed feature (like FORK.md), add a\n")
		b.WriteString("narrow File+Token entry with a reason to guardExceptions and declare it\n")
		b.WriteString("honestly in FORK.md — never a directory or extension exemption.\n")
		t.Error(b.String())
	}
	t.Logf("scanned %s: %d raw hit(s), %d excepted, %d violation(s) in %s",
		root, len(hits), len(kept), len(violations), time.Since(start).Round(time.Millisecond))
}

// TestConfigTypesHaveNoLicenseField (leg 2, reflection) pins ruling R3 and
// decision D1's "legacy key is silently ignored" property: neither config
// type may expose a License field for a rebase to populate.
func TestConfigTypesHaveNoLicenseField(t *testing.T) {
	for _, sample := range []any{config.Config{}, config.Definition{}} {
		typ := reflect.TypeOf(sample)
		if f, ok := typ.FieldByName("License"); ok {
			t.Errorf("%s must not have a License field (ruling R3 / decision D1); found field %q of type %s",
				typ.String(), f.Name, f.Type)
		}
	}
}

// TestEmbeddedSpecHasNoLicenseSurface (leg 3) reads the OpenAPI spec
// compiled into the binary and fails if a stale codegen round reintroduced
// a /license/ path, a License-named operation or a License-named schema.
// The non-empty sanity check keeps this leg from passing vacuously.
func TestEmbeddedSpecHasNoLicenseSurface(t *testing.T) {
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("api.GetSwagger(): %v", err)
	}
	if spec.Paths == nil || spec.Paths.Len() == 0 {
		t.Fatalf("embedded OpenAPI spec has no paths — this leg cannot be trusted")
	}

	var problems []string
	for _, p := range spec.Paths.Keys() {
		if strings.HasPrefix(p, "/license/") {
			problems = append(problems, "path "+p)
		}
		if pi := spec.Paths.Value(p); pi != nil {
			for method, op := range pi.Operations() {
				if op != nil && strings.Contains(op.OperationID, "License") {
					problems = append(problems,
						fmt.Sprintf("operation %s %s (operationId %q)", strings.ToUpper(method), p, op.OperationID))
				}
			}
		}
	}
	if spec.Components != nil {
		var schemas []string
		for name := range spec.Components.Schemas {
			schemas = append(schemas, name)
		}
		sort.Strings(schemas)
		for _, name := range schemas {
			if strings.Contains(name, "License") {
				problems = append(problems, "schema "+name)
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Errorf("embedded OpenAPI spec exposes %d license surface(s):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	t.Logf("embedded spec: %d path(s) checked, %d problem(s)", spec.Paths.Len(), len(problems))
}

// moduleRoot walks up from the test's working directory to the go.mod that
// anchors the tree the scan must cover.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
