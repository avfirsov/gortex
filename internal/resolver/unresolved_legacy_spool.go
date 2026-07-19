package resolver

import (
	"bufio"
	"encoding/gob"
	"errors"
	"io"
	"os"

	"github.com/zzet/gortex/internal/graph"
)

type unresolvedEdgeRecord struct {
	From     string
	To       string
	Kind     graph.EdgeKind
	FilePath string
	Line     int
}

// unresolvedLegacySpool gives stores without a native high-water pager the
// same stable pass boundary without a pointer snapshot. The initial predicate
// iterator is drained into compact identities before mutations begin; replay
// rehydrates each bounded page with one batched site lookup.
type unresolvedLegacySpool struct {
	store   graph.Store
	file    *os.File
	writer  *bufio.Writer
	encoder *gob.Encoder
	decoder *gob.Decoder
	count   int
}

func newUnresolvedLegacySpool(store graph.Store) (*unresolvedLegacySpool, error) {
	file, err := os.CreateTemp("", "gortex-unresolved-*")
	if err != nil {
		return nil, err
	}
	writer := bufio.NewWriterSize(file, 256<<10)
	spool := &unresolvedLegacySpool{
		store: store, file: file, writer: writer, encoder: gob.NewEncoder(writer),
	}
	for edge := range store.EdgesWithUnresolvedTarget() {
		if edge == nil {
			continue
		}
		record := unresolvedEdgeRecord{
			From: edge.From, To: edge.To, Kind: edge.Kind,
			FilePath: edge.FilePath, Line: edge.Line,
		}
		if err := spool.encoder.Encode(&record); err != nil {
			spool.close()
			return nil, err
		}
		spool.count++
	}
	if err := spool.writer.Flush(); err != nil {
		spool.close()
		return nil, err
	}
	if _, err := spool.file.Seek(0, io.SeekStart); err != nil {
		spool.close()
		return nil, err
	}
	spool.decoder = gob.NewDecoder(bufio.NewReaderSize(spool.file, 256<<10))
	return spool, nil
}

func (s *unresolvedLegacySpool) close() {
	if s == nil || s.file == nil {
		return
	}
	_ = s.writer.Flush()
	name := s.file.Name()
	_ = s.file.Close()
	_ = os.Remove(name)
	s.file = nil
}

func (s *unresolvedLegacySpool) nextPage(maxRows, maxBytes int) ([]*graph.Edge, bool, error) {
	if maxRows <= 0 {
		maxRows = resolvePendingPageRows
	}
	if maxBytes <= 0 {
		maxBytes = resolvePendingPageBytes
	}
	records := make([]unresolvedEdgeRecord, 0, maxRows)
	bytesUsed := 0
	exhausted := false
	for len(records) < maxRows && bytesUsed < maxBytes {
		var record unresolvedEdgeRecord
		if err := s.decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				exhausted = true
				break
			}
			return nil, false, err
		}
		records = append(records, record)
		bytesUsed += 96 + len(record.From) + len(record.To) + len(record.Kind) + len(record.FilePath)
	}
	if len(records) == 0 {
		return nil, exhausted, nil
	}
	sites := make([]graph.EdgeSite, 0, len(records))
	for _, record := range records {
		sites = append(sites, graph.EdgeSite{From: record.From, Line: record.Line, Kind: record.Kind})
	}
	candidates := s.store.GetEdgeCandidates(nil, sites)
	edges := make([]*graph.Edge, 0, len(records))
	for _, record := range records {
		for _, edge := range candidates.Site(record.From, record.Line, record.Kind) {
			if edge != nil && edge.To == record.To && edge.FilePath == record.FilePath {
				edges = append(edges, edge)
				break
			}
		}
	}
	return edges, exhausted, nil
}
