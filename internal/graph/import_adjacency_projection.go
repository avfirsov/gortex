package graph

// ImportAdjacencyProjector is an optional narrow read capability for stores
// that can project direct import adjacency without materialising every node in
// a caller file and every outgoing edge kind. The result maps each requested
// caller file to the direct import target IDs recorded for that file.
//
// complete is false when the store cannot prove that the projection preserves
// normal Store adjacency semantics (for example, malformed source provenance
// or a read error). Callers must then use the ordinary Store methods. Missing
// files and files with no imports may be absent from the result when complete
// is true.
type ImportAdjacencyProjector interface {
	ProjectImportAdjacency(filePaths []string) (targetsByFile map[string][]string, complete bool)
}
