package indexer

import "testing"

func TestIsTestFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"internal/indexer/indexer_test.go", true},
		{"cmd/gortex/main.go", false},
		{"src/components/Button.test.tsx", true},
		{"src/components/Button.spec.ts", true},
		{"src/components/Button.tsx", false},
		{"tests/test_user.py", true},
		{"app/test_helpers.py", true},
		{"app/helpers.py", false},
		{"lib/user_test.dart", true},
		{"lib/user.dart", false},
		{"spec/user_spec.rb", true},
		{"app/user.rb", false},
		{"src/UserTest.java", true},
		{"src/User.java", false},
		{"src/UserTests.kt", true},
		{"src/User.kt", false},
		{"X/Y/__tests__/Z.ts", true},
		{"X/Y/__tests__/Z.js", true},
		{"X/test/foo.go", true}, // dir marker overrides extension miss
		{"src/UserTests.cs", true},
		{"src/UserTests.swift", true},
		{"src/UserTest.php", true},
	}
	for _, c := range cases {
		if got := IsTestFile(c.path); got != c.want {
			t.Errorf("IsTestFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsTestSymbol(t *testing.T) {
	cases := []struct {
		name, lang string
		want       bool
	}{
		{"TestFoo", "go", true},
		{"BenchmarkFoo", "go", true},
		{"FuzzFoo", "go", true},
		{"ExampleFoo", "go", true},
		{"Test", "go", false}, // bare prefix doesn't match
		{"Testing", "go", false},
		{"Helper", "go", false},
		{"test_foo", "python", true},
		{"TestFoo", "python", true},
		{"helper", "python", false},
		{"test_foo", "ruby", true},
		{"helper", "ruby", false},
		{"foo", "rust", false},
		{"testFoo", "java", false}, // annotation-driven
	}
	for _, c := range cases {
		if got := IsTestSymbol(c.name, c.lang); got != c.want {
			t.Errorf("IsTestSymbol(%q,%q) = %v, want %v", c.name, c.lang, got, c.want)
		}
	}
}
