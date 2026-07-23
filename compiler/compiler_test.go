package compiler

import (
	"errors"
	"strings"
	"testing"

	"github.com/operatorstack/interlock/ir"
	"github.com/operatorstack/interlock/spec"
)

func validSpec() spec.Spec {
	return spec.Spec{
		PolicyID: "p.v1",
		Actors:   []spec.Actor{{ID: "agent"}, {ID: "publisher"}},
		Resources: []spec.Resource{
			{ID: "artifact", Kind: ir.KindFile, URI: "repo://out/x.json"},
		},
		Rules: []spec.Rule{
			{ID: "deny-agent", Effect: ir.EffectDeny, Actor: "agent", Operations: []ir.Operation{ir.OpWrite}, Resource: "artifact"},
			{ID: "allow-pub", Effect: ir.EffectAllow, Actor: "publisher", Operations: []ir.Operation{ir.OpPublish}, Resource: "artifact",
				Requires: []ir.Requirement{{Kind: ir.ReqPolicyHashMatch}}},
		},
	}
}

func TestCompileValid(t *testing.T) {
	p, err := Compile(validSpec())
	if err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	if p.Protocol != ir.Protocol || len(p.Rules) != 2 {
		t.Fatalf("unexpected IR: %+v", p)
	}
}

func TestCompileDeterministic(t *testing.T) {
	// Same spec built twice, plus actor/resource order shuffled, must hash equal.
	p1, _ := Compile(validSpec())
	s2 := validSpec()
	s2.Actors = []spec.Actor{{ID: "publisher"}, {ID: "agent"}} // reversed
	p2, _ := Compile(s2)
	h1, _ := p1.Hash()
	h2, _ := p2.Hash()
	if h1 != h2 {
		t.Fatalf("actor declaration order leaked into hash: %s vs %s", h1, h2)
	}
}

func TestCompileRejects(t *testing.T) {
	cases := map[string]func(*spec.Spec){
		"blank policy id": func(s *spec.Spec) { s.PolicyID = "" },
		"dup actor":       func(s *spec.Spec) { s.Actors = append(s.Actors, spec.Actor{ID: "agent"}) },
		"dup resource": func(s *spec.Spec) {
			s.Resources = append(s.Resources, spec.Resource{ID: "artifact", Kind: ir.KindFile, URI: "y"})
		},
		"unknown actor":    func(s *spec.Spec) { s.Rules[0].Actor = "ghost" },
		"unknown resource": func(s *spec.Spec) { s.Rules[0].Resource = "ghost" },
		"unknown op":       func(s *spec.Spec) { s.Rules[0].Operations = []ir.Operation{"filesystem.teleport"} },
		"empty ops":        func(s *spec.Spec) { s.Rules[0].Operations = nil },
		"dup rule id":      func(s *spec.Spec) { s.Rules[1].ID = "deny-agent" },
		"require on deny":  func(s *spec.Spec) { s.Rules[0].Requires = []ir.Requirement{{Kind: ir.ReqPolicyHashMatch}} },
		"conflicting effects": func(s *spec.Spec) {
			s.Rules[1] = spec.Rule{ID: "conflict", Effect: ir.EffectAllow, Actor: "agent", Operations: []ir.Operation{ir.OpWrite}, Resource: "artifact"}
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSpec()
			mut(&s)
			_, err := Compile(s)
			if err == nil {
				t.Fatalf("expected rejection for %q", name)
			}
			var d *Diagnostics
			if !errors.As(err, &d) {
				t.Fatalf("want *Diagnostics, got %T", err)
			}
		})
	}
}

func TestUnreachableRuleDetected(t *testing.T) {
	s := validSpec()
	// Add a second deny with the same matcher as rule 0 → shadowed.
	s.Rules = append(s.Rules, spec.Rule{ID: "shadowed", Effect: ir.EffectDeny, Actor: "agent", Operations: []ir.Operation{ir.OpWrite}, Resource: "artifact"})
	_, err := Compile(s)
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("expected unreachable diagnostic, got %v", err)
	}
}
