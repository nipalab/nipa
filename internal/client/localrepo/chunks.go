package localrepo

import (
	"fmt"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const chunkHashSize = 32

func encodeChunkHashes(hashes []serverDomain.Hash) []byte {
	out := make([]byte, 0, len(hashes)*chunkHashSize)
	for _, h := range hashes {
		out = append(out, h[:]...)
	}
	return out
}

func decodeChunkHashes(data []byte) ([]serverDomain.Hash, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data)%chunkHashSize != 0 {
		return nil, fmt.Errorf("invalid local chunk hash data (length %d)", len(data))
	}
	hashes := make([]serverDomain.Hash, 0, len(data)/chunkHashSize)
	for i := 0; i < len(data); i += chunkHashSize {
		var h serverDomain.Hash
		copy(h[:], data[i:i+chunkHashSize])
		hashes = append(hashes, h)
	}
	return hashes, nil
}
