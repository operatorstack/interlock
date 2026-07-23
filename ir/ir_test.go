package ir

import "testing"

func samplePolicy() Policy {
	return Policy{
		Protocol: Protocol,
		PolicyID: "p.v1",
		Actors:   []string{"agent", "publisher"},
		Resources: []Resource{
			{ID: "artifact", Kind: KindFile, URI: "repo://out/x.json"},
		},
		Rules: []Rule{
			{ID: "deny-agent", Effect: EffectDeny, Actor: "agent", Operations: []Operation{OpWrite}, Resource: "artifact"},
		},
	}
}

func TestCanonicalIsStable(t *testing.T) {
	p := samplePolicy()
	a, err := Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonical not stable:\n%s\n%s", a, b)
	}
}

func TestCanonicalSortsKeys(t *testing.T) {
	// Two maps with keys inserted in different orders must canonicalize equal.
	m1 := map[string]any{"b": 1, "a": 2, "c": map[string]any{"z": 1, "y": 2}}
	m2 := map[string]any{"c": map[string]any{"y": 2, "z": 1}, "a": 2, "b": 1}
	c1, _ := Canonical(m1)
	c2, _ := Canonical(m2)
	if string(c1) != string(c2) {
		t.Fatalf("key order leaked into canonical form:\n%s\n%s", c1, c2)
	}
}

func TestHashTagged(t *testing.T) {
	h, err := samplePolicy().Hash()
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != len("sha256:")+64 || h[:7] != "sha256:" {
		t.Fatalf("unexpected hash form: %q", h)
	}
}

func TestEquivalentPoliciesHashEqual(t *testing.T) {
	p1 := samplePolicy()
	p2 := samplePolicy()
	h1, _ := p1.Hash()
	h2, _ := p2.Hash()
	if h1 != h2 {
		t.Fatalf("equivalent policies hash differently: %s vs %s", h1, h2)
	}
}

func TestDifferentPoliciesHashDiffer(t *testing.T) {
	p1 := samplePolicy()
	p2 := samplePolicy()
	p2.PolicyID = "other.v1"
	h1, _ := p1.Hash()
	h2, _ := p2.Hash()
	if h1 == h2 {
		t.Fatal("distinct policies collided")
	}
}
