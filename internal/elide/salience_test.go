package elide

import (
	"strings"
	"testing"
)

const salienceFixtureGo = `package svc

import "fmt"

// Process does straight-line work with some branching.
func Process(n int) int {
	a := 1
	b := 2
	c := 3
	d := 4
	total := a + b + c + d
	if total > 5 {
		fmt.Println("big")
		total = total * 2
		total = total + 1
	}
	for i := 0; i < n; i++ {
		total = total + i
		total = total - 1
	}
	e := total + 10
	f := e + 20
	return f
}
`

func TestSalienceTruncate_NoOpUnderBudget(t *testing.T) {
	out, truncated, err := SalienceTruncate([]byte(salienceFixtureGo), "go", 1000)
	if err != nil {
		t.Fatalf("SalienceTruncate: %v", err)
	}
	if truncated {
		t.Error("source under budget must not be truncated")
	}
	if string(out) != salienceFixtureGo {
		t.Error("under-budget source must be returned verbatim")
	}
}

func TestSalienceTruncate_DisabledWhenMaxLinesZero(t *testing.T) {
	out, truncated, err := SalienceTruncate([]byte(salienceFixtureGo), "go", 0)
	if err != nil {
		t.Fatalf("SalienceTruncate: %v", err)
	}
	if truncated || string(out) != salienceFixtureGo {
		t.Error("max_lines=0 must disable truncation")
	}
}

func TestSalienceTruncate_KeepsControlFlowSkeleton(t *testing.T) {
	out, truncated, err := SalienceTruncate([]byte(salienceFixtureGo), "go", 18)
	if err != nil {
		t.Fatalf("SalienceTruncate: %v", err)
	}
	if !truncated {
		t.Fatal("over-budget source must be truncated")
	}
	got := string(out)
	checkContains(t, got, []string{
		"package svc",
		`import "fmt"`,
		"func Process(n int) int {",
		"if total > 5 {",           // control-flow header kept
		"for i := 0; i < n; i++ {", // control-flow header kept
		"return f",                 // jump kept
		"lines elided",             // leaf runs collapsed to markers
	}, []string{
		"a := 1",             // straight-line leaf statement dropped
		`fmt.Println("big")`, // leaf inside the if dropped
		"total = total + i",  // leaf inside the for dropped
		"e := total + 10",    // trailing leaf dropped
	})
	// The skeleton must stay within the budget.
	if n := len(strings.Split(got, "\n")); n > 18 {
		t.Errorf("skeleton has %d lines, over the max_lines=18 budget", n)
	}
}

func TestSalienceTruncate_Python(t *testing.T) {
	src := `def process(n):
    a = 1
    b = 2
    c = 3
    if n > 0:
        print("pos")
        a = a + 1
    for i in range(n):
        b = b + i
    return a + b + c
`
	out, truncated, err := SalienceTruncate([]byte(src), "python", 10)
	if err != nil {
		t.Fatalf("SalienceTruncate: %v", err)
	}
	if !truncated {
		t.Fatal("over-budget python source must be truncated")
	}
	got := string(out)
	checkContains(t, got, []string{
		"def process(n):",
		"if n > 0:",
		"for i in range(n):",
		"return a + b + c",
		"# ", // python markers use the # comment prefix
		"lines elided",
	}, []string{
		"a = 1",
		`print("pos")`,
		"b = b + i",
	})
}

func TestSalienceTruncate_UnsupportedLanguageHeadCut(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("line ")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	out, truncated, err := SalienceTruncate([]byte(b.String()), "klingon", 10)
	if err == nil {
		t.Fatal("expected an advisory error for an unsupported language")
	}
	if !truncated {
		t.Fatal("an over-budget unsupported file must still be head-cut")
	}
	got := string(out)
	if !strings.Contains(got, "line 0") || !strings.Contains(got, "line 9") {
		t.Errorf("head cut must keep the first lines, got:\n%s", got)
	}
	if strings.Contains(got, "line 20") {
		t.Errorf("head cut must drop the tail, got:\n%s", got)
	}
	if !strings.Contains(got, "max_lines budget") {
		t.Errorf("head cut must mark the dropped tail, got:\n%s", got)
	}
}

func TestSalienceTruncate_SkeletonStillOverBudgetHeadCut(t *testing.T) {
	// 40 single-line ifs: every if + brace line is salient, so the
	// skeleton barely shrinks and must fall back to a head cut.
	var b strings.Builder
	b.WriteString("package big\n\nfunc Many(x int) int {\n")
	for i := 0; i < 40; i++ {
		b.WriteString("\tif x > ")
		b.WriteString(itoa(i))
		b.WriteString(" {\n\t\tx++\n\t}\n")
	}
	b.WriteString("\treturn x\n}\n")

	out, truncated, err := SalienceTruncate([]byte(b.String()), "go", 30)
	if err != nil {
		t.Fatalf("SalienceTruncate: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncation")
	}
	got := string(out)
	if n := len(strings.Split(got, "\n")); n > 31 {
		t.Errorf("skeleton over budget must be head-cut to <= maxLines+marker, got %d lines", n)
	}
	if !strings.Contains(got, "max_lines budget") {
		t.Errorf("expected the budget marker, got:\n%s", got)
	}
}

func TestSalienceTruncate_EmptySource(t *testing.T) {
	out, truncated, err := SalienceTruncate(nil, "go", 10)
	if err != nil {
		t.Errorf("SalienceTruncate(nil) error = %v", err)
	}
	if truncated || len(out) != 0 {
		t.Error("empty source must be a no-op")
	}
}
