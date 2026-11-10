package daemon

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgressAdapter_UploadVerify(t *testing.T) {
	p := newProgressAdapter()
	p.UploadVerify(65)

	ev := <-p.events()
	progress := ev.GetProgress()
	require.Equal(t, "verify", progress.GetPhase())
	require.EqualValues(t, 65, progress.GetObjectsTotal())
}
