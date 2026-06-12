package rerank

// CoChangeSignal scores by logical-coupling cohesion: how strongly a
// candidate's file co-changes (per git history) with the files of the
// other candidates in the same result batch. A candidate whose file
// repeatedly ships alongside other surfaced files is more likely to
// be part of the coherent slice of code the agent is after.
//
// Depends on Context.CoChangeOf, populated by the MCP server from the
// EdgeCoChange enrichment. When the hook is nil — no co-change data
// mined yet — the signal contributes 0, exactly like churn before a
// blame pass.
type CoChangeSignal struct{}

func (CoChangeSignal) Name() string { return SignalCoChange }

func (CoChangeSignal) Contribute(_ string, c *Candidate, ctx *Context) float64 {
	if ctx.CoChangeOf == nil || c == nil || c.Node == nil {
		return 0
	}
	file := c.Node.FilePath
	if file == "" || len(ctx.fileGroups) <= 1 {
		return 0
	}
	neighbors := ctx.CoChangeOf(file)
	if len(neighbors) == 0 {
		return 0
	}
	// Average the co-change score over every other batch file this
	// candidate's file is coupled to. Each score is already in [0,1];
	// the mean keeps the contribution bounded and rewards a candidate
	// that sits at the centre of a coupled cluster.
	var total float64
	var n int
	for fp := range ctx.fileGroups {
		if fp == file {
			continue
		}
		if score := neighbors[fp]; score > 0 {
			total += score
			n++
		}
	}
	if n == 0 {
		return 0
	}
	avg := total / float64(n)
	if avg > 1 {
		avg = 1
	}
	return avg
}
