package publish

import (
	"strings"
	"testing"

	"github.com/operatorstack/interlock/ir"
)

// testPolicy is a minimal policy declaring two distinct resources by ID. It is
// enough to exercise resource resolution without running the engine.
func testPolicy() Policy {
	return ir.Policy{
		Protocol: ir.Protocol,
		PolicyID: "resolve-test.v1",
		Actors:   []string{"publisher"},
		Resources: []ir.Resource{
			{ID: "artifact", Kind: ir.KindFile, URI: "repo://out/result.json"},
			{ID: "workspace", Kind: ir.KindTree, URI: "repo://work/**"},
		},
	}
}

func TestResolveResource(t *testing.T) {
	p := testPolicy()

	uri, kind, err := ResolveResource(p, "artifact")
	if err != nil {
		t.Fatalf("resolve artifact: %v", err)
	}
	if uri != "repo://out/result.json" || kind != KindFile {
		t.Fatalf("resolve artifact = (%q,%q), want (repo://out/result.json,file)", uri, kind)
	}

	if _, _, err := ResolveResource(p, "workspace"); err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
}

func TestResolveResourceUnknownIDFailsClosed(t *testing.T) {
	_, _, err := ResolveResource(testPolicy(), "does-not-exist")
	if err == nil {
		t.Fatal("resolving an undeclared resource id must fail closed")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error = %q, want it to mention the id is not declared", err)
	}
}

func TestResolveResourceDuplicateIDFailsClosed(t *testing.T) {
	p := testPolicy()
	p.Resources = append(p.Resources, ir.Resource{ID: "artifact", Kind: ir.KindFile, URI: "repo://other.json"})

	_, _, err := ResolveResource(p, "artifact")
	if err == nil {
		t.Fatal("a duplicate resource id must fail closed rather than pick arbitrarily")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("error = %q, want it to mention the duplicate", err)
	}
}

// Resolution runs before any filesystem I/O, so an unknown ResourceID surfaces
// even with a bogus staged path — proving the request never reaches the broker.
func TestPublishUnknownResourceIDFailsBeforeIO(t *testing.T) {
	_, err := Publish(Request{
		Policy:     testPolicy(),
		RunID:      "r1",
		RequestID:  "req1",
		Actor:      "publisher",
		ResourceID: "missing",
		StagedPath: "/nonexistent/staged",
		TargetPath: "/nonexistent/target",
	})
	if err == nil {
		t.Fatal("publish with an undeclared ResourceID must fail closed")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error = %q, want a resolution error, not an I/O error", err)
	}
}

// An explicit ResourceURI that disagrees with the resolved declaration must fail
// closed — the façade never silently overrides the policy's source of truth.
func TestPublishConflictingExplicitURIFailsClosed(t *testing.T) {
	_, err := Publish(Request{
		Policy:      testPolicy(),
		RunID:       "r1",
		RequestID:   "req1",
		Actor:       "publisher",
		ResourceID:  "artifact",
		ResourceURI: "repo://out/WRONG.json",
		StagedPath:  "/nonexistent/staged",
		TargetPath:  "/nonexistent/target",
	})
	if err == nil {
		t.Fatal("a conflicting explicit ResourceURI must fail closed")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("error = %q, want a conflict error", err)
	}
}
