package lfs

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func oidOf(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func TestParsePointer(t *testing.T) {
	oid := oidOf("hello")
	p, ok := ParsePointer([]byte(Pointer{OID: oid, Size: 5}.String()))
	require.True(t, ok)
	assert.Equal(t, Pointer{OID: oid, Size: 5}, p)

	legacy := "version https://hawser.github.com/spec/v1\noid sha256:" + oid + "\nsize 5"
	p, ok = ParsePointer([]byte(legacy))
	require.True(t, ok)
	assert.Equal(t, int64(5), p.Size)

	ext := "version https://git-lfs.github.com/spec/v1\next-0-foo sha256:" + oid + "\noid sha256:" + oid + "\nsize 0\n"
	p, ok = ParsePointer([]byte(ext))
	require.True(t, ok)
	assert.Zero(t, p.Size)

	head := "version https://git-lfs.github.com/spec/v1\n"
	bad := map[string]string{
		"empty":       "",
		"too big":     head + "oid sha256:" + oid + "\nsize 5\n" + strings.Repeat("x", MaxPointerSize),
		"short":       head + "size 5\n",
		"no version":  "oid sha256:" + oid + "\nsize 5\nx y\n",
		"bad version": "version https://example.com/v2\noid sha256:" + oid + "\nsize 5\n",
		"bad oid":     head + "oid sha256:" + strings.ToUpper(oid) + "\nsize 5\n",
		"other algo":  head + "oid md5:" + oid + "\nsize 5\n",
		"short oid":   head + "oid sha256:abc\nsize 5\n",
		"negative":    head + "oid sha256:" + oid + "\nsize -1\n",
		"plus":        head + "oid sha256:" + oid + "\nsize +5\n",
		"not number":  head + "oid sha256:" + oid + "\nsize five\n",
		"unsorted":    head + "size 5\noid sha256:" + oid + "\n",
		"unknown key": head + "oid sha256:" + oid + "\nsize 5\nzzz 1\n",
		"no size":     head + "oid sha256:" + oid + "\next-1 x\n",
		"no oid":      head + "ext-0-a b\nsize 5\n",
		"blank value": head + "oid \nsize 5\n",
		"no space":    head + "oid\nsize 5\n",
		"duplicate":   head + "oid sha256:" + oid + "\noid sha256:" + oid + "\nsize 5\n",
		"dup size":    head + "oid sha256:" + oid + "\nsize 5\nsize 6\n",
		"plain text":  "hello world\nthis is text\nnot a pointer\n",
	}
	for name, text := range bad {
		_, ok := ParsePointer([]byte(text))
		assert.False(t, ok, name)
	}
	assert.True(t, ValidOID(oid))
	assert.False(t, ValidOID(oid[:63]+"g"))
}
