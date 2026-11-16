package storageusage

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func TestFormatBytes(t *testing.T) {
	const gib = int64(1) << 30
	cases := map[int64]string{
		0:                        "0 B",
		1023:                     "1023 B",
		1536:                     "1.5 KiB",
		12 * 1024 * 1024:         "12 MiB",
		15 * gib:                 "15 GiB",
		gib + gib/5:              "1.2 GiB",
		3 * 1024 * gib:           "3.0 TiB",
		2048 * 1024 * 1024 * gib: "2048 PiB",
	}
	for in, want := range cases {
		require.Equal(t, want, FormatBytes(in), in)
	}
}

func TestQuotaExceededMessage(t *testing.T) {
	const gib = int64(1) << 30
	err := quotaExceeded(14*gib+gib/2, 15*gib, 2*gib, "https://nipalab.com/settings/billing")
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, 402, domainErr.Code)
	require.Equal(t, "storage quota exceeded: 14 GiB of 15 GiB used and this upload adds 2.0 GiB; the organization's billing owner can add storage at https://nipalab.com/settings/billing", domainErr.Message)

	err = quotaExceeded(gib, gib, 1, "")
	require.EqualError(t, err, "storage quota exceeded: 1.0 GiB of 1.0 GiB used and this upload adds 1 B; free space or ask the organization's billing owner for more storage")
}
