package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestObjCExtractor_Basics(t *testing.T) {
	src := []byte(`#import <Foundation/Foundation.h>
#import "Helper.h"

@interface Greeter : NSObject
- (void)greet:(NSString *)name;
+ (instancetype)sharedInstance;
@end

@implementation Greeter
- (void)greet:(NSString *)name {
    NSLog(@"hi %@", name);
}
+ (instancetype)sharedInstance {
    return nil;
}
@end

static int helper(int x) {
    return x + 1;
}
`)
	e := NewObjCExtractor()
	require.Equal(t, "objc", e.Language())

	res, err := e.Extract("greet.m", src)
	require.NoError(t, err)

	var gotGreeter, gotGreetSel, gotShared, gotHelper bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Greeter":
			gotGreeter = true
		case "greet:":
			gotGreetSel = true
		case "sharedInstance":
			gotShared = true
		case "helper":
			gotHelper = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Foundation/Foundation.h" {
			gotImport = true
		}
	}
	assert.True(t, gotGreeter)
	assert.True(t, gotGreetSel)
	assert.True(t, gotShared)
	assert.True(t, gotHelper)
	assert.True(t, gotImport)
}

func TestObjCExtractor_EmptyInput(t *testing.T) {
	res, err := NewObjCExtractor().Extract("e.m", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
