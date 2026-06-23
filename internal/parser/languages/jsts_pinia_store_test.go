package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// piniaPlaceholder returns the store-factory placeholder edge for a given
// store_action, or nil.
func piniaPlaceholder(edges []*graph.Edge, action string) *graph.Edge {
	for _, e := range edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "store-factory" {
			continue
		}
		if sa, _ := e.Meta["store_action"].(string); sa == action {
			return e
		}
	}
	return nil
}

func TestPinia_OptionsStoreInstanceCall(t *testing.T) {
	src := `import { defineStore } from 'pinia';
export const useUserStore = defineStore('user', {
  actions: { login(u) { return u; } },
});
const user = useUserStore();
function go() { user.login('x'); }
`
	res, err := NewTypeScriptExtractor().Extract("store.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	e := piniaPlaceholder(res.Edges, "login")
	if e == nil {
		t.Fatalf("no store-factory placeholder for user.login()")
	}
	if e.From != "store.ts::go" {
		t.Errorf("placeholder From = %q (want store.ts::go)", e.From)
	}
	if sb, _ := e.Meta["store_binding"].(string); sb != "useUserStore" {
		t.Errorf("store_binding = %q (want useUserStore)", sb)
	}
}

func TestPinia_SetupStoreActionsTagged(t *testing.T) {
	src := `import { defineStore } from 'pinia';
export const useUserStore = defineStore('user', () => {
  function login(u) { return u; }
  return { login };
});
const user = useUserStore();
function go() { user.login('x'); }
`
	res, err := NewTypeScriptExtractor().Extract("store.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// The setup-store action is tagged so it joins the store-factory index.
	var tagged bool
	for _, n := range res.Nodes {
		if n.Name == "login" && n.Meta != nil {
			if sf, _ := n.Meta["store_factory"].(string); sf == "useUserStore" {
				tagged = true
			}
		}
	}
	if !tagged {
		t.Errorf("setup-store login action not tagged with store_factory")
	}
	if piniaPlaceholder(res.Edges, "login") == nil {
		t.Errorf("no store-factory placeholder for setup-store user.login()")
	}
}

func TestPinia_VueSFCScriptSetupCallerScope(t *testing.T) {
	// The .vue <script setup> body must materialise an enclosing function so
	// the placeholder's From is non-empty.
	vue := `<script setup>
import { useUserStore } from '@/stores/user';
const user = useUserStore();
function go() { user.login('x'); }
</script>
<template><button @click="go">go</button></template>
`
	res, err := NewVueExtractor().Extract("Profile.vue", []byte(vue))
	if err != nil {
		t.Fatal(err)
	}
	e := piniaPlaceholder(res.Edges, "login")
	if e == nil {
		t.Fatalf("no store-factory placeholder from a .vue SFC")
	}
	if e.From == "" {
		t.Errorf("placeholder From is empty — the SFC script has no caller scope")
	}
	if sb, _ := e.Meta["store_binding"].(string); sb != "useUserStore" {
		t.Errorf("store_binding = %q (want useUserStore)", sb)
	}
}

func TestPinia_NonStoreGetterIgnored(t *testing.T) {
	// A plain `useThing()` (not a *Store getter) must not produce a
	// store-factory placeholder.
	src := `const t = useThing();
function go() { t.run(); }
`
	res, err := NewTypeScriptExtractor().Extract("x.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if piniaPlaceholder(res.Edges, "run") != nil {
		t.Errorf("non-store getter must not produce a store-factory placeholder")
	}
}
