package contracts

import (
	"testing"
)

// TestTSPromiseInlineEnvelope_Guards is the regression for the
// `Promise<{ guards: Guard[] }>` shape — the previous regex missed
// inline-object Promise types and produced an empty schema. The new
// envelope extractor splits the inner object into one row per key
// and tags list shapes (`Guard[]`) as repeated.
func TestTSPromiseInlineEnvelope_Guards(t *testing.T) {
	body := `  guards: async (): Promise<{ guards: Guard[] }> => {
    const res = await serverFetch('/v1/guards')
    return res.json()
  },`
	env := tsPromiseInlineEnvelope(body, nil)
	if len(env) != 1 {
		t.Fatalf("envelope rows: want 1, got %d", len(env))
	}
	row := env[0]
	if row.Name != "guards" {
		t.Errorf("name: want guards, got %q", row.Name)
	}
	if !row.Repeated {
		t.Errorf("repeated: want true, got false")
	}
	if row.Type != "Guard" {
		t.Errorf("type: want Guard (no fileNodes → bare name), got %q", row.Type)
	}
}

// TestTSPromiseInlineEnvelope_MultiKey covers a multi-key inline
// object: `Promise<{ communities: Community[]; modularity: number }>`.
func TestTSPromiseInlineEnvelope_MultiKey(t *testing.T) {
	body := `  communities: async (): Promise<{ communities: Community[]; modularity: number }> => {
    const res = await serverFetch('/v1/communities')
    return res.json()
  },`
	env := tsPromiseInlineEnvelope(body, nil)
	if len(env) != 2 {
		t.Fatalf("envelope rows: want 2, got %d (%+v)", len(env), env)
	}
	if env[0].Name != "communities" || !env[0].Repeated || env[0].Type != "Community" {
		t.Errorf("row 0: %+v", env[0])
	}
	if env[1].Name != "modularity" || env[1].Repeated || env[1].Type != "number" {
		t.Errorf("row 1: %+v", env[1])
	}
}

// TestTSPromiseInlineEnvelope_PlainPromiseStillUnmatched confirms
// that the inline-object regex doesn't mis-match plain
// `Promise<Foo>` returns — those should fall through to
// tsPromiseReturnRe which handles bare identifiers.
func TestTSPromiseInlineEnvelope_PlainPromiseStillUnmatched(t *testing.T) {
	body := `  health: async (): Promise<HealthResponse> => {
    return await serverFetch('/v1/health')
  },`
	env := tsPromiseInlineEnvelope(body, nil)
	if len(env) != 0 {
		t.Errorf("plain Promise<X> must NOT produce envelope; got %+v", env)
	}
}

// TestSplitTSObjectField_RejectsMethodShorthand guards against
// `compute(x: number): number` being mis-parsed as a JSON key
// `compute(x` with type `number): number`. Method shorthand isn't
// part of the JSON-envelope shape, so we reject it.
func TestSplitTSObjectField_RejectsMethodShorthand(t *testing.T) {
	if _, _, ok := splitTSObjectField(`compute(x: number): number`); ok {
		t.Error("method shorthand should not parse as a key:type entry")
	}
	if _, _, ok := splitTSObjectField(`'quoted-key': string`); !ok {
		t.Error("quoted key should parse")
	}
	if k, v, ok := splitTSObjectField(`name?: string`); !ok || k != "name" || v != "string" {
		t.Errorf("optional name: ok=%v k=%q v=%q", ok, k, v)
	}
}
