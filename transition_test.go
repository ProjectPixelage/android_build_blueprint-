// Copyright 2024 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package blueprint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func testTransitionCommon(bp string, neverFar bool, ctxHook func(*Context)) (*Context, []error) {
	ctx := newContext()
	ctx.MockFileSystem(map[string][]byte{
		"Android.bp": []byte(bp),
	})

	ctx.RegisterBottomUpMutator("deps", depsMutator)
	handle := ctx.RegisterTransitionMutator("transition", transitionTestMutator{})
	if neverFar {
		handle.NeverFar()
	}
	ctx.RegisterBottomUpMutator("post_transition_deps", postTransitionDepsMutator).UsesReverseDependencies()

	ctx.RegisterModuleType("transition_module", newTransitionModule)

	if ctxHook != nil {
		ctxHook(ctx)
	}

	_, errs := ctx.ParseBlueprintsFiles("Android.bp", nil)
	if len(errs) > 0 {
		return nil, errs
	}

	_, errs = ctx.ResolveDependencies(nil)
	if len(errs) > 0 {
		return nil, errs
	}

	return ctx, nil
}

func testTransition(bp string) (*Context, []error) {
	return testTransitionCommon(bp, false, nil)
}

func testTransitionNeverFar(bp string) (*Context, []error) {
	return testTransitionCommon(bp, true, nil)
}

func testTransitionAllowMissingDeps(bp string) (*Context, []error) {
	return testTransitionCommon(bp, false, func(ctx *Context) {
		ctx.SetAllowMissingDependencies(true)
	})
}

func assertNoErrors(t *testing.T, errs []error) {
	t.Helper()
	if len(errs) > 0 {
		t.Errorf("unexpected errors:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}
}

func assertOneErrorMatches(t *testing.T, errs []error, re string) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("expected 1 error, but found 0")
	}
	if len(errs) > 1 {
		t.Errorf("expected exactly 1 error, but found:")
		for _, err := range errs {
			t.Errorf("  %s", err)
		}
		t.FailNow()
	}
	if match, err := regexp.MatchString(re, errs[0].Error()); err != nil {
		t.Fatal(err.Error())
	} else if !match {
		t.Fatalf("expected error matching %q, but got: %q", re, errs[0].Error())
	}
}

const testTransitionBp = `
			transition_module {
			    name: "A",
			    deps: ["B", "C"],
				split: ["b", "a"],
			}

			transition_module {
				name: "B",
				deps: ["C"],
				outgoing: "c",
				%s
			}

			transition_module {
				name: "C",
				deps: ["D"],
			}

			transition_module {
				name: "D",
				incoming: "d",
				deps: ["E"],
			}

			transition_module {
				name: "E",
			}

			transition_module {
				name: "F",
				incoming: "",
			}

			transition_module {
				name: "G",
				outgoing: "h",
				%s
			}

			transition_module {
				name: "H",
				split: ["h"],
			}
		`

func getTransitionModule(ctx *Context, name, variant string) *transitionModule {
	group := ctx.moduleGroupFromName(name, nil)
	module := group.moduleByVariantName(variant)
	return module.logicModule.(*transitionModule)
}

func checkTransitionVariants(t *testing.T, ctx *Context, name string, expectedVariants []string) {
	t.Helper()
	group := ctx.moduleGroupFromName(name, nil)
	var gotVariants []string
	for _, module := range group.modules {
		gotVariants = append(gotVariants, module.variant.variations.get("transition"))
	}
	if !slices.Equal(expectedVariants, gotVariants) {
		t.Errorf("expected variants of %q to be %q, got %q", name, expectedVariants, gotVariants)
	}
}

func checkTransitionDeps(t *testing.T, ctx *Context, m Module, expected ...string) {
	t.Helper()
	var got []string
	ctx.VisitDirectDeps(m, func(m Module) {
		got = append(got, ctx.ModuleName(m)+"("+ctx.ModuleSubDir(m)+")")
	})
	if !slices.Equal(got, expected) {
		t.Errorf("unexpected %q dependencies, got %q expected %q",
			ctx.ModuleName(m), got, expected)
	}
}

func checkTransitionMutate(t *testing.T, m *transitionModule, variant string) {
	t.Helper()
	if m.properties.Mutated != variant {
		t.Errorf("unexpected mutated property in %q, expected %q got %q", m.Name(), variant, m.properties.Mutated)
	}
}

func TestTransition(t *testing.T) {
	ctx, errs := testTransition(fmt.Sprintf(testTransitionBp, "", ""))
	assertNoErrors(t, errs)

	// Module A uses Split to create a and b variants
	checkTransitionVariants(t, ctx, "A", []string{"b", "a"})
	// Module B inherits a and b variants from A
	checkTransitionVariants(t, ctx, "B", []string{"", "a", "b"})
	// Module C inherits a and b variants from A, but gets an outgoing c variant from B
	checkTransitionVariants(t, ctx, "C", []string{"", "a", "b", "c"})
	// Module D always has incoming variant d
	checkTransitionVariants(t, ctx, "D", []string{"", "d"})
	// Module E inherits d from D
	checkTransitionVariants(t, ctx, "E", []string{"", "d"})
	// Module F is untouched
	checkTransitionVariants(t, ctx, "F", []string{""})

	A_a := getTransitionModule(ctx, "A", "a")
	A_b := getTransitionModule(ctx, "A", "b")
	B_a := getTransitionModule(ctx, "B", "a")
	B_b := getTransitionModule(ctx, "B", "b")
	C_a := getTransitionModule(ctx, "C", "a")
	C_b := getTransitionModule(ctx, "C", "b")
	C_c := getTransitionModule(ctx, "C", "c")
	D_d := getTransitionModule(ctx, "D", "d")
	E_d := getTransitionModule(ctx, "E", "d")
	F := getTransitionModule(ctx, "F", "")
	G := getTransitionModule(ctx, "G", "")
	H_h := getTransitionModule(ctx, "H", "h")

	checkTransitionDeps(t, ctx, A_a, "B(a)", "C(a)")
	checkTransitionDeps(t, ctx, A_b, "B(b)", "C(b)")
	checkTransitionDeps(t, ctx, B_a, "C(c)")
	checkTransitionDeps(t, ctx, B_b, "C(c)")
	checkTransitionDeps(t, ctx, C_a, "D(d)")
	checkTransitionDeps(t, ctx, C_b, "D(d)")
	checkTransitionDeps(t, ctx, C_c, "D(d)")
	checkTransitionDeps(t, ctx, D_d, "E(d)")
	checkTransitionDeps(t, ctx, E_d)
	checkTransitionDeps(t, ctx, F)
	checkTransitionDeps(t, ctx, G)
	checkTransitionDeps(t, ctx, H_h)

	checkTransitionMutate(t, A_a, "a")
	checkTransitionMutate(t, A_b, "b")
	checkTransitionMutate(t, B_a, "a")
	checkTransitionMutate(t, B_b, "b")
	checkTransitionMutate(t, C_a, "a")
	checkTransitionMutate(t, C_b, "b")
	checkTransitionMutate(t, C_c, "c")
	checkTransitionMutate(t, D_d, "d")
	checkTransitionMutate(t, E_d, "d")
	checkTransitionMutate(t, F, "")
	checkTransitionMutate(t, G, "")
	checkTransitionMutate(t, H_h, "h")
}

func TestPostTransitionDeps(t *testing.T) {
	ctx, errs := testTransition(fmt.Sprintf(testTransitionBp,
		`post_transition_deps: ["C", "D:late", "E:d", "F"],`,
		`post_transition_deps: ["H"],`))
	assertNoErrors(t, errs)

	// Module A uses Split to create a and b variants
	checkTransitionVariants(t, ctx, "A", []string{"b", "a"})
	// Module B inherits a and b variants from A
	checkTransitionVariants(t, ctx, "B", []string{"", "a", "b"})
	// Module C inherits a and b variants from A, but gets an outgoing c variant from B
	checkTransitionVariants(t, ctx, "C", []string{"", "a", "b", "c"})
	// Module D always has incoming variant d
	checkTransitionVariants(t, ctx, "D", []string{"", "d"})
	// Module E inherits d from D
	checkTransitionVariants(t, ctx, "E", []string{"", "d"})
	// Module F is untouched
	checkTransitionVariants(t, ctx, "F", []string{""})

	A_a := getTransitionModule(ctx, "A", "a")
	A_b := getTransitionModule(ctx, "A", "b")
	B_a := getTransitionModule(ctx, "B", "a")
	B_b := getTransitionModule(ctx, "B", "b")
	C_a := getTransitionModule(ctx, "C", "a")
	C_b := getTransitionModule(ctx, "C", "b")
	C_c := getTransitionModule(ctx, "C", "c")
	D_d := getTransitionModule(ctx, "D", "d")
	E_d := getTransitionModule(ctx, "E", "d")
	F := getTransitionModule(ctx, "F", "")
	G := getTransitionModule(ctx, "G", "")
	H_h := getTransitionModule(ctx, "H", "h")

	checkTransitionDeps(t, ctx, A_a, "B(a)", "C(a)")
	checkTransitionDeps(t, ctx, A_b, "B(b)", "C(b)")
	// Verify post-mutator dependencies added to B.  The first C(c) is a pre-mutator dependency.
	//  C(c) was added by C and rewritten by OutgoingTransition on B
	//  D(d) was added by D:late and rewritten by IncomingTransition on D
	//  E(d) was added by E:d
	//  F() was added by F and rewritten OutgoingTransition on B and then IncomingTransition on F
	checkTransitionDeps(t, ctx, B_a, "C(c)", "C(c)", "D(d)", "E(d)", "F()")
	checkTransitionDeps(t, ctx, B_b, "C(c)", "C(c)", "D(d)", "E(d)", "F()")
	checkTransitionDeps(t, ctx, C_a, "D(d)")
	checkTransitionDeps(t, ctx, C_b, "D(d)")
	checkTransitionDeps(t, ctx, C_c, "D(d)")
	checkTransitionDeps(t, ctx, D_d, "E(d)")
	checkTransitionDeps(t, ctx, E_d)
	checkTransitionDeps(t, ctx, F)
	checkTransitionDeps(t, ctx, G, "H(h)")
	checkTransitionDeps(t, ctx, H_h)

	checkTransitionMutate(t, A_a, "a")
	checkTransitionMutate(t, A_b, "b")
	checkTransitionMutate(t, B_a, "a")
	checkTransitionMutate(t, B_b, "b")
	checkTransitionMutate(t, C_a, "a")
	checkTransitionMutate(t, C_b, "b")
	checkTransitionMutate(t, C_c, "c")
	checkTransitionMutate(t, D_d, "d")
	checkTransitionMutate(t, E_d, "d")
	checkTransitionMutate(t, F, "")
	checkTransitionMutate(t, G, "")
	checkTransitionMutate(t, H_h, "h")
}

func TestPostTransitionReverseDeps(t *testing.T) {
	ctx, errs := testTransition(`
		transition_module {
			name: "A",
			split: ["a1", "a2"],
		}

		transition_module {
			name: "B",
			split: ["a1", "a2"],
			post_transition_reverse_deps: ["A"],
		}
	`)
	assertNoErrors(t, errs)

	checkTransitionVariants(t, ctx, "A", []string{"a1", "a2"})
	checkTransitionVariants(t, ctx, "B", []string{"a1", "a2"})

	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "A", "a1"), "B(a1)")
	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "A", "a2"), "B(a2)")
	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "B", "a1"))
	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "B", "a2"))
}

func TestPostTransitionReverseVariationDeps(t *testing.T) {
	ctx, errs := testTransition(`
		transition_module {
			name: "A",
			split: ["a"],
		}

		transition_module {
			name: "B",
			split: ["b"],
			post_transition_reverse_variation_deps: ["A(a)"],
		}
	`)
	assertNoErrors(t, errs)

	checkTransitionVariants(t, ctx, "A", []string{"a"})
	checkTransitionVariants(t, ctx, "B", []string{"b"})

	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "A", "a"), "B(b)")
	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "B", "b"))
}

func TestFarVariationDep(t *testing.T) {
	ctx, errs := testTransition(`
		transition_module {
			name: "A",
			split: ["a"],
			deps: ["B"],
		}
		transition_module {
			name: "B",
			split: ["", "a"],
		}
		transition_module {
			name: "C",
			split: ["c"],
			post_transition_far_deps: ["D"],
		}
		transition_module {
			name: "D",
			split: ["", "c"],
		}
	`)
	assertNoErrors(t, errs)

	checkTransitionVariants(t, ctx, "A", []string{"a"})
	checkTransitionVariants(t, ctx, "B", []string{"", "a"})
	checkTransitionVariants(t, ctx, "C", []string{"c"})
	checkTransitionVariants(t, ctx, "D", []string{"", "c"})

	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "A", "a"), "B(a)")
	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "C", "c"), "D()")
}

func TestNeverFarFarVariationDep(t *testing.T) {
	ctx, errs := testTransitionNeverFar(`
		transition_module {
			name: "A",
			split: ["a"],
			deps: ["B"],
		}
		transition_module {
			name: "B",
			split: ["", "a"],
		}
		transition_module {
			name: "C",
			split: ["c"],
			post_transition_far_deps: ["D"],
		}
		transition_module {
			name: "D",
			split: ["", "c"],
		}
	`)
	assertNoErrors(t, errs)

	checkTransitionVariants(t, ctx, "A", []string{"a"})
	checkTransitionVariants(t, ctx, "B", []string{"", "a"})
	checkTransitionVariants(t, ctx, "C", []string{"c"})
	checkTransitionVariants(t, ctx, "D", []string{"", "c"})

	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "A", "a"), "B(a)")
	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "C", "c"), "D(c)")
}

func TestPostTransitionReverseDepsErrorOnMissingDep(t *testing.T) {
	_, errs := testTransition(`
		transition_module {
			name: "A",
			split: ["a"],
		}

		transition_module {
			name: "B",
			split: ["b"],
			post_transition_reverse_deps: ["A"],
		}
	`)
	assertOneErrorMatches(t, errs, `reverse dependency "A" of "B" missing variant:\s*transition:b\s*available variants:\s*transition:a`)
}

func TestErrorInIncomingTransition(t *testing.T) {
	_, errs := testTransition(`
		transition_module {
			name: "A",
			split: ["a"],
			deps: ["B"],
		}
		transition_module {
			name: "B",
			split: ["a"],
			incoming_transition_error: "my incoming transition error",
		}
	`)
	assertOneErrorMatches(t, errs, "my incoming transition error")
}

func TestErrorInOutgoingTransition(t *testing.T) {
	_, errs := testTransition(`
		transition_module {
			name: "A",
			split: ["a"],
			deps: ["B"],
			outgoing_transition_error: "my outgoing transition error",
		}
		transition_module {
			name: "B",
			split: ["a"],
		}
	`)
	assertOneErrorMatches(t, errs, "my outgoing transition error")
}

func TestPostTransitionReverseDepsAllowMissingDeps(t *testing.T) {
	_, errs := testTransitionAllowMissingDeps(`
		transition_module {
			name: "A",
			split: ["a"],
		}

		transition_module {
			name: "B",
			split: ["b"],
			post_transition_reverse_deps: ["A"],
		}
	`)
	assertNoErrors(t, errs)
}

func TestPostTransitionDepsMissingVariant(t *testing.T) {
	// TODO: eventually this will create the missing variant on demand
	_, errs := testTransition(fmt.Sprintf(testTransitionBp,
		`post_transition_deps: ["E:missing"],`, ""))
	expectedError := `Android.bp:8:4: dependency "E" of "B" missing variant:
  transition:missing
available variants:
  <empty variant>
  transition:d`
	if len(errs) != 1 || errs[0].Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, errs)
	}
}

func TestIsAddingDependency(t *testing.T) {
	ctx, errs := testTransition(`
		transition_module {
			name: "A",
			split: ["a1"],
			deps: ["C"],
		}

		transition_module {
			name: "B",
			split: ["b1"],
			post_transition_deps: ["C"],
		}

		transition_module {
			name: "C",
			split: ["c1", "c2"],
			incoming: "c1",
			post_transition_incoming: "c2",
		}
	`)
	assertNoErrors(t, errs)

	checkTransitionVariants(t, ctx, "A", []string{"a1"})
	checkTransitionVariants(t, ctx, "B", []string{"b1"})
	checkTransitionVariants(t, ctx, "C", []string{"c1", "c2"})

	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "A", "a1"), "C(c1)")
	checkTransitionDeps(t, ctx, getTransitionModule(ctx, "B", "b1"), "C(c2)")
}

type transitionTestMutator struct{}

func (transitionTestMutator) Split(ctx BaseModuleContext) []string {
	if split := ctx.Module().(*transitionModule).properties.Split; len(split) > 0 {
		return split
	}
	return []string{""}
}

func (transitionTestMutator) OutgoingTransition(ctx OutgoingTransitionContext, sourceVariation string) string {
	if err := ctx.Module().(*transitionModule).properties.Outgoing_transition_error; err != nil {
		ctx.ModuleErrorf("Error: %s", *err)
	}
	if outgoing := ctx.Module().(*transitionModule).properties.Outgoing; outgoing != nil {
		return *outgoing
	}
	return sourceVariation
}

func (transitionTestMutator) IncomingTransition(ctx IncomingTransitionContext, incomingVariation string) string {
	if err := ctx.Module().(*transitionModule).properties.Incoming_transition_error; err != nil {
		ctx.ModuleErrorf("Error: %s", *err)
	}
	if ctx.IsAddingDependency() {
		if incoming := ctx.Module().(*transitionModule).properties.Post_transition_incoming; incoming != nil {
			return *incoming
		}
	}
	if incoming := ctx.Module().(*transitionModule).properties.Incoming; incoming != nil {
		return *incoming
	}
	return incomingVariation
}

func (transitionTestMutator) Mutate(ctx BottomUpMutatorContext, variation string) {
	ctx.Module().(*transitionModule).properties.Mutated = variation
}

type transitionModule struct {
	SimpleName
	properties struct {
		Deps                                   []string
		Post_transition_deps                   []string
		Post_transition_far_deps               []string
		Post_transition_reverse_deps           []string
		Post_transition_reverse_variation_deps []string
		Split                                  []string
		Outgoing                               *string
		Incoming                               *string
		Post_transition_incoming               *string
		Outgoing_transition_error              *string
		Incoming_transition_error              *string

		Mutated string `blueprint:"mutated"`
	}
}

func newTransitionModule() (Module, []interface{}) {
	m := &transitionModule{}
	return m, []interface{}{&m.properties, &m.SimpleName.Properties}
}

func (f *transitionModule) GenerateBuildActions(ModuleContext) {
}

func (f *transitionModule) Deps() []string {
	return f.properties.Deps
}

func (f *transitionModule) IgnoreDeps() []string {
	return nil
}

var nameAndVariantRegexp = regexp.MustCompile(`([a-zA-Z0-9_]+)\(([a-zA-Z0-9_]+)\)`)

func postTransitionDepsMutator(mctx BottomUpMutatorContext) {
	if m, ok := mctx.Module().(*transitionModule); ok {
		for _, dep := range m.properties.Post_transition_deps {
			module, variation, _ := strings.Cut(dep, ":")
			var variations []Variation
			if variation != "" {
				variations = append(variations, Variation{"transition", variation})
			}
			mctx.AddVariationDependencies(variations, walkerDepsTag{follow: true}, module)
		}
		for _, dep := range m.properties.Post_transition_far_deps {
			mctx.AddFarVariationDependencies(nil, walkerDepsTag{follow: true}, dep)
		}
		for _, dep := range m.properties.Post_transition_reverse_deps {
			mctx.AddReverseDependency(m, walkerDepsTag{follow: true}, dep)
		}
		for _, dep := range m.properties.Post_transition_reverse_variation_deps {
			match := nameAndVariantRegexp.FindStringSubmatch(dep)
			if len(match) == 0 || match[0] != dep {
				panic(fmt.Sprintf("Invalid Post_transition_reverse_variation_deps: %q. Expected module_name(variant)", dep))
			}
			mctx.AddReverseVariationDependency([]Variation{
				{Mutator: "transition", Variation: match[2]},
			}, walkerDepsTag{follow: true}, match[1])
		}
	}
}
