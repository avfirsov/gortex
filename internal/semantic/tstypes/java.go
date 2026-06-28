package tstypes

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
	"github.com/zzet/gortex/internal/parser/tsitter/java"
)

// JavaSpec adapts the engine to tree-sitter-java. Types are explicit
// everywhere, so the binder grounds receivers from parameter / local /
// field annotations and `new` expressions; `implements` / `extends`
// clauses come straight off the declaration.
func JavaSpec() *LangSpec {
	grammar := java.GetLanguage()
	return &LangSpec{
		ProviderName: "java-types",
		Languages:    []string{"java"},
		GrammarFor:   func(string) *sitter.Language { return grammar },
		TypeDeclTypes: map[string]bool{
			"class_declaration":     true,
			"interface_declaration": true,
			"enum_declaration":      true,
		},
		FuncDeclTypes: map[string]bool{
			"method_declaration":      true,
			"constructor_declaration": true,
		},
		SelfName:     "this",
		TypeDeclName: nameField,
		Supertypes:   javaSupertypes,
		Fields:       javaFields,
		Params:       javaParams,
		ReturnType: func(fn *sitter.Node, src []byte) string {
			if fn.Type() != "method_declaration" {
				return ""
			}
			return fieldText(fn, "type", src)
		},
		LocalBinding: javaLocalBinding,
		Call:         javaCall,
		CallArgCount: javaCallArgCount,
		NewExprType: func(n *sitter.Node, src []byte) string {
			if n.Type() != "object_creation_expression" {
				return ""
			}
			return fieldText(n, "type", src)
		},
		FieldRef: func(n *sitter.Node, src []byte) (string, bool) {
			if n.Type() != "field_access" {
				return "", false
			}
			obj := n.ChildByFieldName("object")
			if obj == nil || obj.Type() != "this" {
				return "", false
			}
			return fieldText(n, "field", src), true
		},
		Imports: javaImports,
		// A higher-order collection call whose lambda's first parameter is the
		// element type (`xs.forEach(x -> x.foo())`) binds that parameter to the
		// receiver's element type and re-walks the body, so an inner member call
		// on the element resolves.
		CollectionLambda: javaCollectionLambda,
	}
}

func javaSupertypes(n *sitter.Node, src []byte) []SuperRef {
	var out []SuperRef
	switch n.Type() {
	case "class_declaration":
		if sup := n.ChildByFieldName("superclass"); sup != nil {
			for c := range sup.NamedChildren() {
				switch c.Type() {
				case "type_identifier", "generic_type", "scoped_type_identifier":
					out = append(out, SuperRef{Name: c.Content(src), Kind: graph.EdgeExtends, Line: nodeLine(c)})
				}
			}
		}
		if ifaces := n.ChildByFieldName("interfaces"); ifaces != nil {
			out = append(out, javaTypeList(ifaces, src, graph.EdgeImplements)...)
		}
	case "interface_declaration":
		// `interface A extends B, C` — extends_interfaces is an unnamed
		// field in the grammar; scan direct children.
		for i := 0; i < int(n.ChildCount()); i++ {
			if c := n.Child(i); c != nil && c.Type() == "extends_interfaces" {
				out = append(out, javaTypeList(c, src, graph.EdgeExtends)...)
			}
		}
	case "enum_declaration":
		if ifaces := n.ChildByFieldName("interfaces"); ifaces != nil {
			out = append(out, javaTypeList(ifaces, src, graph.EdgeImplements)...)
		}
	}
	return out
}

// javaTypeList flattens a super_interfaces / extends_interfaces node's
// type_list into SuperRefs.
func javaTypeList(n *sitter.Node, src []byte, kind graph.EdgeKind) []SuperRef {
	var out []SuperRef
	var visit func(c *sitter.Node)
	visit = func(c *sitter.Node) {
		if c == nil {
			return
		}
		switch c.Type() {
		case "type_list":
			for child := range c.NamedChildren() {
				visit(child)
			}
		case "type_identifier", "generic_type", "scoped_type_identifier":
			out = append(out, SuperRef{Name: c.Content(src), Kind: kind, Line: nodeLine(c)})
		}
	}
	for child := range n.NamedChildren() {
		visit(child)
	}
	return out
}

func javaFields(n *sitter.Node, src []byte) []Binding {
	body := n.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	var out []Binding
	for c := range body.NamedChildren() {
		if c.Type() != "field_declaration" {
			continue
		}
		typ := fieldText(c, "type", src)
		for d := range c.NamedChildren() {
			if d.Type() != "variable_declarator" {
				continue
			}
			name := fieldText(d, "name", src)
			if name == "" {
				continue
			}
			out = append(out, Binding{Name: name, Type: typ, Line: nodeLine(d)})
		}
	}
	return out
}

func javaParams(fn *sitter.Node, src []byte) []Binding {
	params := fn.ChildByFieldName("parameters")
	if params == nil {
		return nil
	}
	var out []Binding
	for p := range params.NamedChildren() {
		switch p.Type() {
		case "formal_parameter", "spread_parameter":
			name := fieldText(p, "name", src)
			if name == "" {
				// spread_parameter puts the variable_declarator last.
				for j := int(p.NamedChildCount()) - 1; j >= 0; j-- {
					if c := p.NamedChild(j); c.Type() == "variable_declarator" {
						name = fieldText(c, "name", src)
						break
					} else if c.Type() == "identifier" {
						name = c.Content(src)
						break
					}
				}
			}
			if name == "" {
				continue
			}
			out = append(out, Binding{Name: name, Type: fieldText(p, "type", src), Line: nodeLine(p)})
		}
	}
	return out
}

func javaLocalBinding(n *sitter.Node, src []byte) (LocalBind, bool) {
	switch n.Type() {
	case "local_variable_declaration":
		decl := firstChildOfType(n, "variable_declarator")
		if decl == nil {
			return LocalBind{}, false
		}
		return LocalBind{
			Name:     fieldText(decl, "name", src),
			DeclType: fieldText(n, "type", src),
			Init:     decl.ChildByFieldName("value"),
		}, true
	case "assignment_expression":
		left := n.ChildByFieldName("left")
		if left == nil || left.Type() != "identifier" {
			return LocalBind{}, false
		}
		return LocalBind{Name: left.Content(src), Init: n.ChildByFieldName("right")}, true
	}
	return LocalBind{}, false
}

func javaCall(n *sitter.Node, src []byte) (*sitter.Node, string, bool) {
	if n.Type() != "method_invocation" {
		return nil, "", false
	}
	obj := n.ChildByFieldName("object")
	if obj == nil {
		return nil, "", false
	}
	return obj, fieldText(n, "name", src), true
}

// javaCallArgCount counts the argument expressions of a method
// invocation (`recv.m(a, b, c)` -> 3, `recv.m()` -> 0). Commas and
// parentheses are anonymous, so the argument_list's named children are
// the arguments; comment nodes that the grammar admits as extras are
// skipped so they never inflate the count.
func javaCallArgCount(n *sitter.Node, _ []byte) (int, bool) {
	if n.Type() != "method_invocation" {
		return 0, false
	}
	args := n.ChildByFieldName("arguments")
	if args == nil {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c != nil && c.Type() == "argument_list" {
				args = c
				break
			}
		}
	}
	if args == nil {
		return 0, false
	}
	count := 0
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "line_comment", "block_comment":
			continue
		}
		count++
	}
	return count, true
}

// javaElementCallbacks is the curated set of Stream / Iterable higher-order
// methods whose lambda's first (and only) parameter is the receiver's element
// type. Kept tiny and honest: a fold (`reduce`) or a comparator-taking
// `sorted` is excluded, so the parameter is never bound to the wrong type.
var javaElementCallbacks = map[string]bool{
	"forEach":        true,
	"forEachOrdered": true,
	"filter":         true,
	"map":            true,
	"anyMatch":       true,
	"allMatch":       true,
	"noneMatch":      true,
	"takeWhile":      true,
	"dropWhile":      true,
	"peek":           true,
	"removeIf":       true,
}

// javaCollectionLambda decodes a higher-order collection call whose lambda's
// first parameter is the receiver's element type — `xs.forEach(x -> x.foo())`.
// It grounds the receiver (the call's `object`) and gates the method on the
// element-callback set, then requires the sole argument to be a
// single-parameter lambda it can decode. The receiver may be a chained call
// (`xs.stream().filter(…)`); the binder declines to element-type a
// chained-call receiver, so that form is walked generically. ok=false for any
// other call.
func javaCollectionLambda(n *sitter.Node, src []byte) (CollectionLambdaCall, bool) {
	if n.Type() != "method_invocation" {
		return CollectionLambdaCall{}, false
	}
	obj := n.ChildByFieldName("object")
	if obj == nil || !javaElementCallbacks[fieldText(n, "name", src)] {
		return CollectionLambdaCall{}, false
	}
	lambda := javaSingleLambdaArg(n)
	if lambda == nil {
		return CollectionLambdaCall{}, false
	}
	param := javaLambdaParam(lambda, src)
	body := lambda.ChildByFieldName("body")
	if param == "" || body == nil {
		return CollectionLambdaCall{}, false
	}
	return CollectionLambdaCall{Receiver: obj, Param: param, Body: body}, true
}

// javaSingleLambdaArg returns the lambda_expression when it is the SOLE
// argument of a method invocation, nil otherwise (zero, multiple, or
// non-lambda arguments). Comment extras the grammar admits are ignored so
// they never inflate the count.
func javaSingleLambdaArg(n *sitter.Node) *sitter.Node {
	args := n.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	var lambda *sitter.Node
	count := 0
	for c := range args.NamedChildren() {
		switch c.Type() {
		case "line_comment", "block_comment":
			continue
		}
		count++
		if c.Type() == "lambda_expression" {
			lambda = c
		}
	}
	if count != 1 {
		return nil
	}
	return lambda
}

// javaLambdaParam returns the single parameter name of a lambda_expression:
// the bare `x -> …` identifier, or the lone identifier / formal parameter of a
// parenthesized `(x) -> …` / `(Foo x) -> …` list. "" when the lambda binds
// more than one parameter — those are not a plain element bind.
func javaLambdaParam(lambda *sitter.Node, src []byte) string {
	params := lambda.ChildByFieldName("parameters")
	if params == nil {
		return ""
	}
	if params.Type() == "identifier" {
		return params.Content(src)
	}
	var only string
	count := 0
	for c := range params.NamedChildren() {
		switch c.Type() {
		case "identifier":
			count++
			only = c.Content(src)
		case "formal_parameter":
			count++
			only = fieldText(c, "name", src)
		}
	}
	if count != 1 {
		return ""
	}
	return only
}

func javaImports(root *sitter.Node, src []byte) []Import {
	var out []Import
	for c := range root.NamedChildren() {
		if c.Type() != "import_declaration" {
			continue
		}
		path := ""
		isWildcard := false
		for j := 0; j < int(c.ChildCount()); j++ {
			ch := c.Child(j)
			if ch == nil {
				continue
			}
			switch ch.Type() {
			case "scoped_identifier", "identifier":
				path = ch.Content(src)
			case "asterisk":
				isWildcard = true
			}
		}
		if path == "" || isWildcard {
			continue
		}
		local := path
		if idx := strings.LastIndex(local, "."); idx >= 0 {
			local = local[idx+1:]
		}
		out = append(out, Import{Local: local, Path: strings.ReplaceAll(path, ".", "/")})
	}
	return out
}
