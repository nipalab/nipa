package provenance

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeCBOR_Values(t *testing.T) {
	v, err := decodeCBOR(cborEncode(map[string]any{
		"u": uint64(1 << 40), "n": -5, "s": "x", "b": []byte{1}, "t": true, "f": false, "z": nil,
		"a": []any{uint64(1), "two"},
	}), 8)
	require.NoError(t, err)
	m := v.(map[string]any)
	require.Equal(t, uint64(1<<40), m["u"])
	require.Equal(t, int64(-5), m["n"])
	require.Equal(t, []byte{1}, m["b"])
	require.Equal(t, true, m["t"])
	require.Equal(t, false, m["f"])
	require.Nil(t, m["z"])
	require.Equal(t, []any{uint64(1), "two"}, m["a"])

	floats := []struct {
		in   []byte
		want float64
	}{
		{[]byte{0xf9, 0x3c, 0x00}, 1},
		{[]byte{0xf9, 0xc0, 0x00}, -2},
		{[]byte{0xf9, 0x00, 0x01}, 5.960464477539063e-08},
		{[]byte{0xf9, 0x80, 0x01}, -5.960464477539063e-08},
		{binary.BigEndian.AppendUint32([]byte{0xfa}, math.Float32bits(1.5)), 1.5},
		{binary.BigEndian.AppendUint64([]byte{0xfb}, math.Float64bits(2.25)), 2.25},
	}
	for _, f := range floats {
		got, err := decodeCBOR(f.in, 4)
		require.NoError(t, err)
		require.InDelta(t, f.want, got, 1e-12)
	}
	inf, err := decodeCBOR([]byte{0xf9, 0x7c, 0x00}, 4)
	require.NoError(t, err)
	require.True(t, math.IsInf(inf.(float64), 1))

	tagged, err := decodeCBOR(append([]byte{0xc1}, cborEncode(uint64(3))...), 4)
	require.NoError(t, err)
	require.Equal(t, uint64(3), tagged)
	undef, err := decodeCBOR([]byte{0xf7}, 4)
	require.NoError(t, err)
	require.Nil(t, undef)

	nonText, err := decodeCBOR(append(cborHead(5, 1), append(cborEncode(uint64(1)), cborEncode("v")...)...), 4)
	require.NoError(t, err)
	require.Empty(t, nonText)
}

func TestDecodeCBOR_Errors(t *testing.T) {
	cases := [][]byte{
		nil,
		{0x1f},
		{0x19, 0x01},
		{0x3b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x43, 0x01},
		{0x63, 'a'},
		{0x9a, 0xff, 0xff, 0xff, 0xff},
		{0xba, 0xff, 0xff, 0xff, 0xff},
		{0x82, 0x01},
		{0xa1, 0x61},
		{0xa1, 0x61, 'k'},
		{0xf8, 0x20},
		{0x9f},
	}
	for _, c := range cases {
		_, err := decodeCBOR(c, 8)
		require.ErrorIs(t, err, errCBOR, "%x", c)
	}
}

func TestDecodeCBOR_DepthAndItemCaps(t *testing.T) {
	deep := make([]byte, 0, 100)
	for range 100 {
		deep = append(deep, 0x81)
	}
	deep = append(deep, 0x00)
	_, err := decodeCBOR(deep, 32)
	require.ErrorContains(t, err, "nesting deeper")

	many := cborHead(4, maxCBORItems+1)
	many = append(many, make([]byte, maxCBORItems+1)...)
	_, err = decodeCBOR(many, 4)
	require.ErrorContains(t, err, "too many items")
}

func TestBoxes_Errors(t *testing.T) {
	_, err := boxes([]byte{0, 0, 0})
	require.ErrorContains(t, err, "truncated box header")
	_, err = boxes([]byte{0, 0, 0, 1, 'j', 'u', 'm', 'b', 0})
	require.ErrorContains(t, err, "truncated box header")
	_, err = boxes([]byte{0, 0, 0, 4, 'j', 'u', 'm', 'b'})
	require.ErrorContains(t, err, "out of range")

	ext := []byte{0, 0, 0, 1, 'f', 'r', 'e', 'e', 0, 0, 0, 0, 0, 0, 0, 17, 'x'}
	got, err := boxes(ext)
	require.NoError(t, err)
	require.Equal(t, "x", string(got[0].payload))
	got, err = boxes([]byte{0, 0, 0, 0, 'f', 'r', 'e', 'e', 'y', 'z'})
	require.NoError(t, err)
	require.Equal(t, "yz", string(got[0].payload))

	var many []byte
	for range maxJUMBFBoxes + 1 {
		many = append(many, rawBox("free", nil)...)
	}
	_, err = boxes(many)
	require.ErrorContains(t, err, "too many boxes")
}

func TestParseSuperbox_Errors(t *testing.T) {
	_, err := parseSuperbox(rawBox("free", nil))
	require.ErrorContains(t, err, "missing description")
	_, err = parseSuperbox(rawBox("jumd", make([]byte, 4)))
	require.ErrorContains(t, err, "short description")
	_, err = parseSuperbox(rawBox("jumd", append(make([]byte, 16), 0x02, 'a')))
	require.ErrorContains(t, err, "unterminated label")
	sb, err := parseSuperbox(rawBox("jumd", append(make([]byte, 16), 0x00)))
	require.NoError(t, err)
	require.Empty(t, sb.label)
	_, err = parseSuperbox([]byte{1})
	require.Error(t, err)
}

func TestParseC2PA_Variants(t *testing.T) {
	lim := Limits{}.withDefaults()
	_, ok, err := parseC2PA(jumbBox("other"), lim)
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = parseC2PA(jumbBox("c2pa"), lim)
	require.NoError(t, err)
	require.False(t, ok)
	_, _, err = parseC2PA(rawBox("free", nil), lim)
	require.ErrorContains(t, err, "not a JUMBF superbox")
	_, _, err = parseC2PA([]byte{1}, lim)
	require.Error(t, err)
	_, _, err = parseC2PA(rawBox("jumb", nil), lim)
	require.Error(t, err)

	noClaim := jumbBox("c2pa", jumbBox("urn:uuid:a", jumbBox("c2pa.assertions"), rawBox("free", nil), rawBox("jumb", []byte{1})))
	_, ok, err = parseC2PA(noClaim, lim)
	require.NoError(t, err)
	require.False(t, ok)

	badClaim := jumbBox("c2pa", jumbBox("urn:uuid:a", jumbBox("c2pa.claim", rawBox("cbor", []byte{0x9f}))))
	_, _, err = parseC2PA(badClaim, lim)
	require.ErrorIs(t, err, errCBOR)
	badAction := jumbBox("c2pa", jumbBox("urn:uuid:a", jumbBox("c2pa.assertions", jumbBox("c2pa.actions", rawBox("cbor", []byte{0x9f})))))
	_, _, err = parseC2PA(badAction, lim)
	require.ErrorIs(t, err, errCBOR)

	composite := c2paStore(
		map[string]any{"claim_generator_info": map[string]any{"name": "Tool"}},
		map[string]any{"actions": []any{"junk", map[string]any{"action": "c2pa.edited"}, map[string]any{"digitalSourceType": "x/compositeWithTrainedAlgorithmicMedia"}}},
	)
	rec, ok, err := parseC2PA(composite, lim)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Tool", rec.Tool)
	require.Equal(t, compositeMedia, rec.DigitalSourceType)

	rec, _, err = parseC2PA(c2paStore(map[string]any{"other": 1}, map[string]any{"actions": "x"}), lim)
	require.NoError(t, err)
	require.Empty(t, rec.Tool)
	require.Empty(t, aiSourceType("x"))
}

func TestCleanText(t *testing.T) {
	require.Equal(t, "a b", cleanText(" a\nb\x7f"))
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	require.Len(t, cleanText(string(long)), maxLabelText)
	require.Equal(t, "ok", cleanText("ok\xff"))
}
