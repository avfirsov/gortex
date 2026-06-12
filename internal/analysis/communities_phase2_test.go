package analysis

import (
	"testing"
)

func TestAssignDirectoryParents_GroupsSiblings(t *testing.T) {
	// Three sibling clusters under parser/languages should share a
	// parent. A solo cluster with a different head gets no parent.
	communities := []Community{
		{ID: "community-1", Label: "parser/languages +5 dirs · GoExtractor"},
		{ID: "community-2", Label: "parser/languages +4 dirs · DartExtractor"},
		{ID: "community-3", Label: "parser/languages · cssExtractor"},
		{ID: "community-4", Label: "contracts · resolveTypeInFile"},
	}
	assignDirectoryParents(communities)
	if communities[0].ParentID == "" || communities[0].ParentID != communities[1].ParentID || communities[0].ParentID != communities[2].ParentID {
		t.Errorf("parser/languages siblings should share a parent; got %q / %q / %q",
			communities[0].ParentID, communities[1].ParentID, communities[2].ParentID)
	}
	if communities[3].ParentID != "" {
		t.Errorf("solo contracts cluster got an invented parent: %q", communities[3].ParentID)
	}
}

func TestAssignDirectoryParents_StripsAllDisambiguators(t *testing.T) {
	// labelHead must peel off every disambiguator suffix the
	// disambiguation cascade can produce: " · sample", " +N dirs",
	// " (N)", " #N".
	cases := map[string]string{
		"parser/languages":                            "parser/languages",
		"parser/languages +5 dirs":                    "parser/languages",
		"parser/languages · GoExtractor":              "parser/languages",
		"parser/languages +5 dirs · GoExtractor":      "parser/languages",
		"parser/languages · GoExtractor (152)":        "parser/languages",
		"parser/languages · GoExtractor (152) #3":     "parser/languages",
		"contracts · resolve":                         "contracts",
	}
	for input, want := range cases {
		got := labelHead(input)
		if got != want {
			t.Errorf("labelHead(%q) = %q; want %q", input, got, want)
		}
	}
}

func TestAssignDirectoryParents_ParentIdsAreStable(t *testing.T) {
	// Parent ids should be a pure function of the head string, so
	// reruns produce identical ids. Important because the UI keys
	// off them and we don't want them shifting on a refresh.
	pre := []Community{
		{ID: "a", Label: "parser/languages +1 dirs · A"},
		{ID: "b", Label: "parser/languages · B"},
	}
	post := []Community{
		{ID: "a", Label: "parser/languages +1 dirs · A"},
		{ID: "b", Label: "parser/languages · B"},
	}
	assignDirectoryParents(pre)
	assignDirectoryParents(post)
	if pre[0].ParentID != post[0].ParentID || pre[1].ParentID != post[1].ParentID {
		t.Errorf("parent ids drifted across runs: %q/%q vs %q/%q",
			pre[0].ParentID, pre[1].ParentID, post[0].ParentID, post[1].ParentID)
	}
	if pre[0].ParentID != "group/parser/languages" {
		t.Errorf("parent id format unexpected: %q (want 'group/parser/languages')", pre[0].ParentID)
	}
}
