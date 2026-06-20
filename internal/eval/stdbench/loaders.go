package stdbench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxJSONLLine caps the scanner buffer — benchmark corpus lines can be
// large code blobs, well past bufio's 64 KiB default.
const maxJSONLLine = 16 * 1024 * 1024

// scanJSONL streams a JSON-lines file, decoding each non-blank line
// into a map and handing it to fn. Blank lines are skipped; a decode
// error is reported with its line number.
func scanJSONL(path string, fn func(rec map[string]any) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxJSONLLine)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(text), &rec); err != nil {
			return fmt.Errorf("%s line %d: %w", path, line, err)
		}
		if err := fn(rec); err != nil {
			return fmt.Errorf("%s line %d: %w", path, line, err)
		}
	}
	return sc.Err()
}

// firstString returns the first string-valued key present in m.
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// LoadCoIR loads a CoIR / BEIR-format benchmark from a directory laid
// out as corpus.jsonl + queries.jsonl + qrels/<split>.tsv. CoIR (Code
// Information Retrieval, ACL 2025) ships every task in this canonical
// BEIR triple. Only queries that appear in the qrels file are kept —
// queries.jsonl typically holds every split at once.
func LoadCoIR(dir string) (Dataset, error) {
	ds := Dataset{Name: "CoIR"}

	corpus, err := loadBEIRCorpus(filepath.Join(dir, "corpus.jsonl"))
	if err != nil {
		return Dataset{}, fmt.Errorf("CoIR corpus: %w", err)
	}
	ds.Corpus = corpus

	queryText, err := loadBEIRQueries(filepath.Join(dir, "queries.jsonl"))
	if err != nil {
		return Dataset{}, fmt.Errorf("CoIR queries: %w", err)
	}

	qrels, err := loadBEIRQrels(dir)
	if err != nil {
		return Dataset{}, fmt.Errorf("CoIR qrels: %w", err)
	}

	ids := make([]string, 0, len(qrels))
	for id := range qrels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ds.Queries = append(ds.Queries, Query{
			ID:       id,
			Text:     queryText[id],
			Relevant: qrels[id],
		})
	}
	return ds, nil
}

func loadBEIRCorpus(path string) ([]Doc, error) {
	var corpus []Doc
	err := scanJSONL(path, func(rec map[string]any) error {
		id := firstString(rec, "_id", "id", "doc_id")
		if id == "" {
			return nil
		}
		title := firstString(rec, "title")
		text := firstString(rec, "text", "content", "body", "code")
		corpus = append(corpus, Doc{ID: id, Text: strings.TrimSpace(title + " " + text)})
		return nil
	})
	return corpus, err
}

func loadBEIRQueries(path string) (map[string]string, error) {
	out := make(map[string]string)
	err := scanJSONL(path, func(rec map[string]any) error {
		id := firstString(rec, "_id", "id", "query_id")
		if id == "" {
			return nil
		}
		out[id] = firstString(rec, "text", "query", "question")
		return nil
	})
	return out, err
}

// loadBEIRQrels reads the first qrels file it finds — qrels/test.tsv,
// qrels/dev.tsv, qrels/train.tsv, or a bare qrels.tsv. The TSV is
// `query-id<TAB>corpus-id<TAB>score`, with an optional header row.
func loadBEIRQrels(dir string) (map[string]map[string]int, error) {
	candidates := []string{
		filepath.Join(dir, "qrels", "test.tsv"),
		filepath.Join(dir, "qrels", "dev.tsv"),
		filepath.Join(dir, "qrels", "train.tsv"),
		filepath.Join(dir, "qrels.tsv"),
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("no qrels file under %s", dir)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	qrels := make(map[string]map[string]int)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxJSONLLine)
	for sc.Scan() {
		fields := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(fields) < 3 {
			continue
		}
		score, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			continue // header row ("query-id corpus-id score") or junk.
		}
		qid, cid := fields[0], fields[1]
		if qrels[qid] == nil {
			qrels[qid] = make(map[string]int)
		}
		qrels[qid][cid] = score
	}
	return qrels, sc.Err()
}

// LoadSWEContextBench loads SWE-ContextBench (arXiv 2602.08316) from a
// JSON-lines file — one context-retrieval task per line.
func LoadSWEContextBench(path string) (Dataset, error) {
	return loadJSONLBench("SWE-ContextBench", path)
}

// LoadContextBench loads ContextBench (arXiv 2602.05892) from a
// JSON-lines file — one context-retrieval task per line.
func LoadContextBench(path string) (Dataset, error) {
	return loadJSONLBench("ContextBench", path)
}

// loadJSONLBench loads a JSON-lines context-retrieval benchmark. Each
// line is one task object:
//
//	{
//	  "id":         "<task id>",            // also task_id / instance_id
//	  "query":      "<natural-language>",   // also question / problem_statement
//	  "relevant":   ["<doc id>", ...],      // also gold / context / expected;
//	                                        //   entries may be {"id","score"}
//	  "candidates": [{"id","text"}, ...]    // optional per-task corpus pool;
//	                                        //   also documents / pool
//	}
//
// Per-task candidate pools are merged (deduplicated by ID) into one
// corpus. A task with no candidates still contributes its query — the
// caller indexes its own corpus in that case.
func loadJSONLBench(name, path string) (Dataset, error) {
	ds := Dataset{Name: name}
	corpusSeen := make(map[string]bool)
	line := 0

	err := scanJSONL(path, func(rec map[string]any) error {
		line++
		q := Query{
			ID:       firstString(rec, "id", "task_id", "instance_id", "_id"),
			Text:     firstString(rec, "query", "question", "problem_statement", "text"),
			Relevant: make(map[string]int),
		}
		if q.ID == "" {
			q.ID = fmt.Sprintf("%s-%d", name, line)
		}
		for _, rs := range relevantIDs(rec, "relevant", "gold", "context", "expected", "gold_files") {
			q.Relevant[rs.id] = rs.score
		}
		for _, doc := range candidateDocs(rec, "candidates", "documents", "pool") {
			if !corpusSeen[doc.ID] {
				corpusSeen[doc.ID] = true
				ds.Corpus = append(ds.Corpus, doc)
			}
		}
		ds.Queries = append(ds.Queries, q)
		return nil
	})
	if err != nil {
		return Dataset{}, err
	}
	return ds, nil
}

// idScore is one relevance judgement extracted from a JSONL task.
type idScore struct {
	id    string
	score int
}

// relevantIDs pulls the relevance judgement list from the first
// matching key. Each list entry is either a bare ID string or an
// {"id","score"} object; a bare string defaults to grade 1.
func relevantIDs(rec map[string]any, keys ...string) []idScore {
	list := firstList(rec, keys...)
	out := make([]idScore, 0, len(list))
	for _, el := range list {
		switch v := el.(type) {
		case string:
			if v != "" {
				out = append(out, idScore{id: v, score: 1})
			}
		case map[string]any:
			id := firstString(v, "id", "_id", "doc_id", "corpus_id")
			if id == "" {
				continue
			}
			score := 1
			if s, ok := v["score"].(float64); ok && int(s) > 0 {
				score = int(s)
			}
			out = append(out, idScore{id: id, score: score})
		}
	}
	return out
}

// candidateDocs pulls the per-task candidate pool from the first
// matching key. Each entry is an {"id","text"} object.
func candidateDocs(rec map[string]any, keys ...string) []Doc {
	list := firstList(rec, keys...)
	out := make([]Doc, 0, len(list))
	for _, el := range list {
		obj, ok := el.(map[string]any)
		if !ok {
			continue
		}
		id := firstString(obj, "id", "_id", "doc_id")
		if id == "" {
			continue
		}
		out = append(out, Doc{ID: id, Text: firstString(obj, "text", "content", "body", "code")})
	}
	return out
}

// firstList returns the first key whose value is a JSON array.
func firstList(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if v, ok := m[k].([]any); ok {
			return v
		}
	}
	return nil
}
