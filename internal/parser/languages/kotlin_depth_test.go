package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestKotlinExtractor_Modifiers(t *testing.T) {
	const kt = `package com.app

class Repo {
    suspend fun load(): String {
        return "x"
    }
}

expect fun platformName(): String

actual fun platformName(): String {
    return "android"
}

fun interface Callback {
    fun onDone()
}
`
	res, err := NewKotlinExtractor().Extract("Repo.kt", []byte(kt))
	if err != nil {
		t.Fatal(err)
	}

	var load, callback *graph.Node
	kmpRoles := map[string]bool{}
	for _, n := range res.Nodes {
		switch n.Name {
		case "load":
			load = n
		case "Callback":
			callback = n
		case "platformName":
			if r, ok := n.Meta["kmp_role"].(string); ok {
				kmpRoles[r] = true
			}
		}
	}

	// suspend functions are flagged async.
	if load == nil {
		t.Fatal("suspend method 'load' was not extracted")
	}
	if load.Meta["is_async"] != true {
		t.Errorf("suspend fun load should be async: meta=%v", load.Meta)
	}

	// expect / actual stamp kmp_role (the marker G5 pairs on).
	if !kmpRoles["expect"] {
		t.Errorf("expect fun platformName should have kmp_role=expect (roles: %v)", kmpRoles)
	}
	if !kmpRoles["actual"] {
		t.Errorf("actual fun platformName should have kmp_role=actual (roles: %v)", kmpRoles)
	}

	// fun interface is recovered as an interface, not a class.
	if callback == nil {
		t.Fatal("fun interface 'Callback' was not extracted")
	}
	if callback.Kind != graph.KindInterface {
		t.Errorf("fun interface Callback should be an interface, got %s", callback.Kind)
	}
}
