package snow

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"
)

const (
	// epoch started at 2026-01-01 00:00:00 UTC
	epoch int64 = 1767225600000

	nodeBits uint8 = 8
	stepBits uint8 = 14
	nodeMax  int64 = -1 ^ (-1 << nodeBits)
	//nodeMask  int64 = nodeMax << stepBits
	stepMask  int64 = -1 ^ (-1 << stepBits)
	timeShift uint8 = nodeBits + stepBits
	nodeShift uint8 = stepBits
)

type Node interface {
	Generate() ID
}

// A Node struct holds the basic information needed for a snowflake generator node.
type node struct {
	mu    sync.Mutex
	epoch time.Time
	time  int64
	node  int64
	step  int64
}

// An ID is a custom type used for a snowflake ID.  This is used so we can
// attach methods onto the ID.
type ID int64

// ErrInvalidNode is returned when an invalid node number is provided.
var ErrInvalidNode = fmt.Errorf("invalid node number")

// NewNode returns a new snowflake node that can be used to generate snowflake
// IDs. Returns ErrInvalidNode if node is out of range.
func NewNode(nodeID int64) (Node, error) {
	if nodeID < 0 || nodeID > nodeMax {
		return nil, ErrInvalidNode
	}

	n := &node{node: nodeID}
	var curTime = time.Now()
	// add time.Duration to curTime to make sure we use the monotonic clock if available
	n.epoch = curTime.Add(time.Unix(epoch/1000, (epoch%1000)*1000000).Sub(curTime))
	return n, nil
}

// Generate creates and returns a unique snowflake ID
func (n *node) Generate() ID {

	n.mu.Lock()
	defer n.mu.Unlock()

	now := time.Since(n.epoch).Milliseconds()

	if now == n.time {
		n.step = (n.step + 1) & stepMask

		if n.step == 0 {
			for now <= n.time {
				now = time.Since(n.epoch).Milliseconds()
			}
		}
	} else {
		n.step = 0
	}

	n.time = now

	r := ID(now<<timeShift |
		(n.node << nodeShift) |
		n.step,
	)

	return r
}

// FloorAt returns the lowest ID a node could generate at t.
func FloorAt(t time.Time) ID {
	ms := t.UnixMilli() - epoch
	if ms <= 0 {
		return 0
	}
	if ms > math.MaxInt64>>timeShift {
		return ID(math.MaxInt64)
	}
	return ID(ms << timeShift)
}

// String returns a string of the snowflake ID
func (f ID) String() string {
	return strconv.FormatInt(int64(f), 10)
}

// ParseString converts a string into a snowflake ID
func ParseString(id string) (ID, error) {
	i, err := strconv.ParseInt(id, 10, 64)
	return ID(i), err
}

func (f ID) Int64() int64 {
	return int64(f)
}

// Base36 returns a base36 string of the snowflake ID
func (f ID) Base36() string {
	return strconv.FormatInt(int64(f), 36)
}

// ParseBase36 converts a Base36 string into a snowflake ID
func ParseBase36(id string) (ID, error) {
	i, err := strconv.ParseInt(id, 36, 64)
	return ID(i), err
}

// MarshalJSON returns a json byte array string of the snowflake ID.
func (f ID) MarshalJSON() ([]byte, error) {
	buff := make([]byte, 0, 22)
	buff = append(buff, '"')
	buff = strconv.AppendInt(buff, int64(f), 36)
	buff = append(buff, '"')
	return buff, nil
}

// UnmarshalJSON converts a json byte array of a snowflake ID into an ID type.
func (f *ID) UnmarshalJSON(b []byte) error {
	if len(b) < 3 || b[0] != '"' || b[len(b)-1] != '"' {
		return JSONSyntaxError{b}
	}

	i, err := strconv.ParseInt(string(b[1:len(b)-1]), 36, 64)
	if err != nil {
		return err
	}

	*f = ID(i)
	return nil
}

// A JSONSyntaxError is returned from UnmarshalJSON if an invalid ID is provided.
type JSONSyntaxError struct{ original []byte }

func (j JSONSyntaxError) Error() string {
	return fmt.Sprintf("invalid snowflake ID %q", string(j.original))
}
