package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestPerlExtractor_Module(t *testing.T) {
	src := []byte(`package My::Module;
use strict;
use warnings;
use List::Util qw(sum);

sub greet {
    my ($name) = @_;
    print "hello $name\n";
    log_call($name);
}

sub log_call {
    my ($n) = @_;
    print STDERR "called with $n\n";
}

1;
`)
	e := NewPerlExtractor()
	require.Equal(t, "perl", e.Language())

	res, err := e.Extract("Module.pm", src)
	require.NoError(t, err)

	var gotPkg, gotGreet, gotLog bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "My::Module":
			gotPkg = true
		case "greet":
			gotGreet = true
		case "log_call":
			gotLog = true
		}
	}
	var gotUse, gotCall bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::strict" {
			gotUse = true
		}
		if ed.Kind == graph.EdgeCalls && ed.To == "unresolved::log_call" {
			gotCall = true
		}
	}
	assert.True(t, gotPkg)
	assert.True(t, gotGreet)
	assert.True(t, gotLog)
	assert.True(t, gotUse)
	assert.True(t, gotCall)
}

func TestPerlExtractor_EmptyInput(t *testing.T) {
	res, err := NewPerlExtractor().Extract("e.pl", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
