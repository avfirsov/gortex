package languages

import (
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// emitCSharpAsyncSpawns walks a C# method body for `await` expressions
// and Task.Run / Task.Factory.StartNew / ThreadPool.QueueUserWorkItem
// calls. Mode is "async" for await, "task" for Task.Run, "thread" for
// thread-pool queues.
func emitCSharpAsyncSpawns(ownerID string, body *sitter.Node, src []byte, filePath string, result *parser.ExtractionResult) {
	if body == nil {
		return
	}
	seen := map[string]bool{}
	emit := func(target, mode string, line int) {
		if target == "" {
			return
		}
		key := mode + "\x00" + target
		if seen[key] {
			return
		}
		seen[key] = true
		result.Edges = append(result.Edges, &graph.Edge{
			From:     ownerID,
			To:       "unresolved::" + target,
			Kind:     graph.EdgeSpawns,
			FilePath: filePath,
			Line:     line,
			Origin:   graph.OriginASTInferred,
			Meta: map[string]any{
				"mode": mode,
			},
		})
	}
	walkCSharpNodes(body, func(n *sitter.Node) bool {
		switch n.Type() {
		case "method_declaration", "lambda_expression", "anonymous_method_expression",
			"local_function_statement":
			return false
		case "await_expression":
			line := int(n.StartPoint().Row) + 1
			// Look for an inner invocation_expression to grab the
			// callee name.
			for i := 0; i < int(n.NamedChildCount()); i++ {
				c := n.NamedChild(i)
				if c == nil {
					continue
				}
				if c.Type() == "invocation_expression" {
					if name := csharpInvocationTargetName(c, src); name != "" {
						emit(name, "async", line)
					}
				}
			}
		case "invocation_expression":
			fn := n.ChildByFieldName("function")
			if fn == nil {
				return true
			}
			line := int(n.StartPoint().Row) + 1
			if fn.Type() == "member_access_expression" {
				expr := fn.ChildByFieldName("expression")
				name := fn.ChildByFieldName("name")
				if expr != nil && name != nil {
					obj := expr.Content(src)
					meth := name.Content(src)
					switch obj {
					case "Task":
						switch meth {
						case "Run", "Factory":
							emit("Task."+meth, "task", line)
						}
					case "Task.Factory":
						if meth == "StartNew" {
							emit("Task.Factory.StartNew", "task", line)
						}
					case "ThreadPool":
						if meth == "QueueUserWorkItem" {
							emit("ThreadPool.QueueUserWorkItem", "thread", line)
						}
					case "Parallel":
						switch meth {
						case "ForEach", "For", "Invoke":
							emit("Parallel."+meth, "parallel", line)
						}
					}
				}
			}
		}
		return true
	})
}

func walkCSharpNodes(n *sitter.Node, visit func(*sitter.Node) bool) {
	if n == nil {
		return
	}
	if !visit(n) {
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkCSharpNodes(n.NamedChild(i), visit)
	}
}

func csharpInvocationTargetName(call *sitter.Node, src []byte) string {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return ""
	}
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "member_access_expression":
		if name := fn.ChildByFieldName("name"); name != nil {
			return name.Content(src)
		}
	case "generic_name":
		if name := fn.ChildByFieldName("name"); name != nil {
			return name.Content(src)
		}
	}
	return ""
}

// csharpFunctionBody returns the body block of a C# method
// declaration. Expression-bodied methods (`=> expr`) have a different
// shape that we don't walk (no spawn-like calls in idiomatic
// expression bodies).
func csharpFunctionBody(methodNode *sitter.Node) *sitter.Node {
	if methodNode == nil {
		return nil
	}
	if b := methodNode.ChildByFieldName("body"); b != nil {
		return b
	}
	for i := 0; i < int(methodNode.NamedChildCount()); i++ {
		c := methodNode.NamedChild(i)
		if c != nil && c.Type() == "block" {
			return c
		}
	}
	return nil
}
