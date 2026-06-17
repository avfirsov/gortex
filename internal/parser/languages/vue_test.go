package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestVueExtractor(t *testing.T) {
	const sfc = `<script setup lang="ts">
import { ref } from 'vue'
const count = ref(0)
function increment() {
  count.value++
}
</script>

<template>
  <button @click="increment">{{ count }}</button>
</template>
`
	res, err := NewVueExtractor().Extract("components/Counter.vue", []byte(sfc))
	if err != nil {
		t.Fatal(err)
	}

	var comp, incr *graph.Node
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType && n.Name == "Counter" {
			comp = n
		}
		if n.Name == "increment" {
			incr = n
		}
	}

	// One always-exported component node named after the file.
	if comp == nil {
		t.Fatalf("no component node 'Counter' among %d nodes", len(res.Nodes))
	}
	if comp.Meta["exported"] != true || comp.Meta["component"] != true {
		t.Errorf("component meta = %v, want exported+component", comp.Meta)
	}
	if comp.Language != "vue" {
		t.Errorf("component language = %q, want vue", comp.Language)
	}

	// The <script setup> logic is delegated to TS and rebased into host coords.
	if incr == nil {
		t.Fatalf("delegated function 'increment' was not extracted from <script setup>")
	}
	if incr.Language != "vue" {
		t.Errorf("delegated symbol language = %q, want vue", incr.Language)
	}
	if incr.Meta["inline_script"] != true {
		t.Errorf("delegated symbol missing inline_script meta: %v", incr.Meta)
	}
	if incr.StartLine != 4 {
		t.Errorf("increment StartLine = %d, want 4 (host-file coordinates)", incr.StartLine)
	}
}
