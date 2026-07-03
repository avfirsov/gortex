package embedding

import "testing"

func TestValidateBatch(t *testing.T) {
	vec := func(n int) []float32 { return make([]float32, n) }

	cases := []struct {
		name    string
		texts   []string
		vecs    [][]float32
		dims    int
		wantErr bool
	}{
		{"ok strict width", []string{"a", "b"}, [][]float32{vec(384), vec(384)}, 384, false},
		{"ok empty batch", []string{}, [][]float32{}, 384, false},
		{"ok lazy width consistent", []string{"a", "b"}, [][]float32{vec(768), vec(768)}, 0, false},
		{"count mismatch short", []string{"a", "b"}, [][]float32{vec(384)}, 384, true},
		{"count mismatch extra", []string{"a"}, [][]float32{vec(384), vec(384)}, 384, true},
		{"nil vector", []string{"a", "b"}, [][]float32{vec(384), nil}, 384, true},
		{"empty vector", []string{"a"}, [][]float32{{}}, 384, true},
		{"wrong strict width", []string{"a"}, [][]float32{vec(100)}, 384, true},
		{"ragged lazy width", []string{"a", "b"}, [][]float32{vec(768), vec(384)}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBatch("test", tc.texts, tc.vecs, tc.dims)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateBatch err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
