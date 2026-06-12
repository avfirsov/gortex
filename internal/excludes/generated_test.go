package excludes

import (
	"reflect"
	"testing"
)

func TestIsGenerated(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"api/user.pb.go", true},
		{"api/user.pb.cc", true},
		{"api/user.pb.h", true},
		{"api/user.pb.swift", true},
		{"proto/user_pb2.py", true},
		{"proto/user_pb2_grpc.py", true},
		{"x_gen.go", true},
		{"x.gen.go", true},
		{"x_generated.go", true},
		{"x.generated.go", true},
		{"model.g.dart", true},
		{"model.freezed.dart", true},
		{"View.g.cs", true},
		{"View.designer.cs", true},
		{"zz_generated.deepcopy.go", true},
		{"store/mock_store.go", true},
		{"store/store_mock.go", true},
		// Windows separators normalise.
		{`api\user.pb.go`, true},
		// Negatives.
		{"api/user.go", false},
		{"store/store.go", false},
		{"genuine.go", false}, // "gen" prefix is not a marker
		{"", false},
	}
	for _, c := range cases {
		if got := IsGenerated(c.path); got != c.want {
			t.Errorf("IsGenerated(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestGeneratedPeerPaths(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"api/user.pb.go", []string{"api/user.go"}},
		{"proto/user_pb2.py", []string{"proto/user.py"}},
		{"proto/user_pb2_grpc.py", []string{"proto/user.py"}},
		{"x_gen.go", []string{"x.go"}},
		{"x.generated.go", []string{"x.go"}},
		{"model.freezed.dart", []string{"model.dart"}},
		{"View.designer.cs", []string{"View.cs"}},
		{"store/mock_store.go", []string{"store/store.go"}},
		{"store/store_mock.go", []string{"store/store.go"}},
		// No clean peer.
		{"zz_generated.deepcopy.go", nil},
		{"api/user.go", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := GeneratedPeerPaths(c.path)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("GeneratedPeerPaths(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
