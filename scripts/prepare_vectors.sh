#!/usr/bin/env bash
# Prepare GloVe word vectors for the StaticProvider.
# Downloads GloVe 6B, extracts top 100k words (300d), converts to binary format.
#
# Usage: ./scripts/prepare_vectors.sh
#
# Output: internal/embedding/data/vectors.bin.gz (~3MB)
# Input:  Downloads glove.6B.zip (~862MB) to /tmp/

set -euo pipefail

GLOVE_URL="https://nlp.stanford.edu/data/glove.6B.zip"
GLOVE_DIR="/tmp/glove"
OUTPUT_DIR="internal/embedding/data"
OUTPUT_FILE="${OUTPUT_DIR}/vectors.bin.gz"
MAX_WORDS=20000
DIMS=50

echo "=== GloVe Vector Preparation ==="

# Download if not cached.
if [ ! -f "${GLOVE_DIR}/glove.6B.${DIMS}d.txt" ]; then
    echo "Downloading GloVe 6B..."
    mkdir -p "${GLOVE_DIR}"
    curl -L -o "${GLOVE_DIR}/glove.6B.zip" "${GLOVE_URL}"
    echo "Extracting..."
    unzip -o "${GLOVE_DIR}/glove.6B.zip" "glove.6B.${DIMS}d.txt" -d "${GLOVE_DIR}"
fi

echo "Converting to binary format (top ${MAX_WORDS} words, ${DIMS}d)..."
mkdir -p "${OUTPUT_DIR}"

python3 -c "
import struct, gzip, sys

input_file = '${GLOVE_DIR}/glove.6B.${DIMS}d.txt'
output_file = '${OUTPUT_FILE}'
max_words = ${MAX_WORDS}
dims = ${DIMS}

words = []
with open(input_file, 'r', encoding='utf-8') as f:
    for i, line in enumerate(f):
        if i >= max_words:
            break
        parts = line.strip().split(' ')
        word = parts[0]
        vec = [float(x) for x in parts[1:]]
        if len(vec) != dims:
            continue
        words.append((word, vec))

print(f'Loaded {len(words)} words')

with gzip.open(output_file, 'wb') as f:
    # Header: word count + dimensions
    f.write(struct.pack('<II', len(words), dims))
    for word, vec in words:
        word_bytes = word.encode('utf-8')
        f.write(struct.pack('<H', len(word_bytes)))
        f.write(word_bytes)
        f.write(struct.pack(f'<{dims}f', *vec))

import os
size_mb = os.path.getsize(output_file) / (1024 * 1024)
print(f'Written {output_file} ({size_mb:.1f} MB)')
"

echo "Done. Add the following to static_data.go:"
echo '  //go:embed data/vectors.bin.gz'
echo '  var vectorData []byte'
