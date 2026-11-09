package snow

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MyStruct struct {
	ID ID `json:"id"`
}

func TestSnow(t *testing.T) {
	node, err := NewNode(1)
	assert.NoError(t, err)
	assert.NotNil(t, node)

	id := node.Generate()
	fmt.Println(id)
	fmt.Println(id.Base36())
	id += 1
	fmt.Println(id.Base36())
	time.Sleep(2 * time.Millisecond)
	id = node.Generate()
	fmt.Println(id.Base36())

	s := MyStruct{ID: node.Generate()}
	jsonByte, err := json.Marshal(&s)
	assert.NoError(t, err)
	fmt.Println(string(jsonByte))

	result := &MyStruct{}
	err = json.Unmarshal(jsonByte, result)
	assert.NoError(t, err)
	assert.Equal(t, s.ID, result.ID)
}

func TestTimestamp(t *testing.T) {
	before, err := time.Parse("2006", "2003")
	assert.NoError(t, err)
	fmt.Println(before.UnixMilli())
	now := time.Since(before).Milliseconds()
	fmt.Println(now)
	second := time.Since(before).Milliseconds() / 1000
	fmt.Println(second)
}

func TestNewNode_AcceptsBoundaryNodeIDs(t *testing.T) {
	for _, nodeID := range []int64{0, nodeMax} {
		node, err := NewNode(nodeID)
		require.NoError(t, err, "nodeID: %d", nodeID)
		require.NotNil(t, node)

		id := node.Generate()
		require.NotZero(t, id)
	}
}

func TestNewNode_RejectsInvalidNodeIDs(t *testing.T) {
	for _, nodeID := range []int64{-1, nodeMax + 1} {
		node, err := NewNode(nodeID)
		require.ErrorIs(t, err, ErrInvalidNode, "nodeID: %d", nodeID)
		require.Nil(t, node)
	}
}

func TestNode_Generate_StrictlyIncreasingAndUnique(t *testing.T) {
	node, err := NewNode(1)
	require.NoError(t, err)

	const count = 5000
	seen := make(map[ID]struct{}, count)

	prev := ID(-1)
	for i := 0; i < count; i++ {
		id := node.Generate()
		require.Greater(t, id, prev, "IDs must be strictly increasing")
		seen[id] = struct{}{}
		prev = id
	}
	require.Len(t, seen, count, "IDs must be unique")
}

func TestNode_Generate_ConcurrentUnique(t *testing.T) {
	node, err := NewNode(7)
	require.NoError(t, err)

	const (
		workers   = 8
		perWorker = 250
	)

	results := make(chan ID, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				results <- node.Generate()
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[ID]struct{}, workers*perWorker)
	for id := range results {
		require.NotZero(t, id)
		require.NotContains(t, seen, id, "duplicate ID generated under concurrency")
		seen[id] = struct{}{}
	}
	require.Len(t, seen, workers*perWorker)
}

func TestNode_Generate_StepExhaustionWaitsForNextMillisecond(t *testing.T) {
	n, err := NewNode(2)
	require.NoError(t, err)
	node := n.(*node)

	// Force the generator to run out of steps within the current millisecond.
	// A clock tick between our read and Generate's own read makes the wrap
	// impossible for that attempt, so retry until we observe it.
	wrapped := false
	for attempt := 0; attempt < 100 && !wrapped; attempt++ {
		now := time.Since(node.epoch).Milliseconds()

		node.mu.Lock()
		node.time = now
		node.step = stepMask
		node.mu.Unlock()

		id := node.Generate()
		if int64(id)&stepMask == 0 {
			wrapped = true
			// After the wrap the ID must encode the next millisecond...
			require.Greater(t, int64(id)>>timeShift, now)
		}
	}
	require.True(t, wrapped, "step exhaustion path was never exercised")
}

func TestID_BitLayout(t *testing.T) {
	const nodeID = 200
	node, err := NewNode(nodeID)
	require.NoError(t, err)

	before := time.Since(epochTime()).Milliseconds()
	id := node.Generate()
	after := time.Since(epochTime()).Milliseconds()

	raw := id.Int64()
	require.GreaterOrEqual(t, raw>>timeShift, before)
	require.LessOrEqual(t, raw>>timeShift, after)
	require.Equal(t, int64(nodeID), (raw>>nodeShift)&nodeMax)
	step := raw & stepMask
	require.GreaterOrEqual(t, step, int64(0))
	require.Less(t, step, stepMask+1)
}

// epochTime returns the configured snowflake epoch as a time.Time.
func epochTime() time.Time {
	return time.Unix(epoch/1000, (epoch%1000)*int64(time.Millisecond))
}

func TestID_StringAndParseString(t *testing.T) {
	node, err := NewNode(3)
	require.NoError(t, err)
	id := node.Generate()

	parsed, err := ParseString(id.String())
	require.NoError(t, err)
	require.Equal(t, id, parsed)

	_, err = ParseString("not-a-number")
	require.Error(t, err)
}

func TestID_Int64(t *testing.T) {
	require.Equal(t, int64(12345), ID(12345).Int64())
}

func TestID_Base36RoundTrip(t *testing.T) {
	node, err := NewNode(4)
	require.NoError(t, err)
	id := node.Generate()

	parsed, err := ParseBase36(id.Base36())
	require.NoError(t, err)
	require.Equal(t, id, parsed)

	require.Equal(t, "73", ID(255).Base36())

	_, err = ParseBase36("!!invalid!!")
	require.Error(t, err)
}

func TestID_MarshalJSON(t *testing.T) {
	b, err := json.Marshal(ID(255))
	require.NoError(t, err)
	require.Equal(t, `"73"`, string(b))

	var got ID
	require.NoError(t, json.Unmarshal([]byte(`"73"`), &got))
	require.Equal(t, ID(255), got)
}

func TestID_UnmarshalJSON_Errors(t *testing.T) {
	cases := [][]byte{
		[]byte(`123`),    // not a JSON string
		[]byte(`""`),     // too short to hold quotes + content
		[]byte(`"73`),    // missing closing quote
		[]byte(`"zzz!"`), // invalid base36 content
	}
	for _, input := range cases {
		var id ID
		err := id.UnmarshalJSON(input)
		require.Error(t, err, "input: %s", input)
	}
}

func TestJSONSyntaxError_Message(t *testing.T) {
	err := JSONSyntaxError{original: []byte(`oops`)}
	require.Equal(t, `invalid snowflake ID "oops"`, err.Error())
}

func TestFloorAt(t *testing.T) {
	start := time.UnixMilli(epoch)
	assert.Equal(t, ID(0), FloorAt(start))
	assert.Equal(t, ID(0), FloorAt(start.Add(-time.Hour)))
	assert.Equal(t, ID(1<<timeShift), FloorAt(start.Add(time.Millisecond)))
	assert.Equal(t, ID(1000<<timeShift), FloorAt(start.Add(time.Second+500*time.Microsecond)))
	assert.Equal(t, ID(math.MaxInt64), FloorAt(time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)))

	n, err := NewNode(nodeMax)
	require.NoError(t, err)
	before := time.Now().Add(-time.Millisecond)
	id := n.Generate()
	after := time.Now().Add(time.Millisecond)
	assert.LessOrEqual(t, FloorAt(before), id)
	assert.Less(t, id, FloorAt(after))
}
