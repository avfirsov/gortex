package embedding

import _ "embed"

// Word vector data: GloVe 6B, 50 dimensions, top 20k words (~3.7MB compressed).
// Format: header (word_count uint32 + dims uint32) + entries (word_len uint16 + word + dims×float32).

//go:embed data/vectors.bin.gz
var vectorData []byte
