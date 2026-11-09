package billing

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsLive(t *testing.T) {
	for status, live := range map[string]bool{
		StatusActive: true, StatusTrialing: true, StatusPastDue: true,
		StatusPaused: false, StatusCanceled: false, "": false, "unknown": false,
	} {
		require.Equal(t, live, IsLive(status), status)
	}
}

func TestEntitlement_StatusTable(t *testing.T) {
	cfg := Config{FreeSeats: 2, FreeQuotaBytes: 100, SeatIncludedBytes: 50, StoragePackBytes: 1000}
	free := Entitlement{SeatLimit: 2, QuotaBytes: 100}
	paid := Entitlement{Paid: true, SeatLimit: 5 + 2, QuotaBytes: 100 + 5*50 + 2*1000}

	require.Equal(t, free, cfg.Entitlement(nil))
	for _, status := range []string{StatusActive, StatusTrialing, StatusPastDue} {
		require.Equal(t, paid, cfg.Entitlement(&Subscription{Status: status, Seats: 5, StoragePacks: 2}), status)
	}
	for _, status := range []string{StatusPaused, StatusCanceled, "bogus"} {
		require.Equal(t, free, cfg.Entitlement(&Subscription{Status: status, Seats: 5, StoragePacks: 2}), status)
	}
}

func TestEntitlement_Formula(t *testing.T) {
	cfg := Config{FreeSeats: 3, FreeQuotaBytes: 1000, SeatIncludedBytes: 100, StoragePackBytes: 500}

	small := cfg.Entitlement(&Subscription{Status: StatusActive, Seats: 1})
	require.Equal(t, int64(1000+100), small.QuotaBytes, "paid storage adds to the free quota")
	require.Equal(t, int64(4), small.SeatLimit, "paid seats add to the free seats")

	negative := cfg.Entitlement(&Subscription{Status: StatusActive, Seats: -4, StoragePacks: -1})
	require.Equal(t, int64(1000), negative.QuotaBytes)
	require.Equal(t, int64(3), negative.SeatLimit)

	packsOnly := cfg.Entitlement(&Subscription{Status: StatusActive, StoragePacks: 2})
	require.Equal(t, Entitlement{Paid: true, SeatLimit: 3, QuotaBytes: 1000 + 2*500}, packsOnly)

	big := cfg.Entitlement(&Subscription{Status: StatusActive, Seats: 10, StoragePacks: 3})
	require.Equal(t, int64(1000+10*100+3*500), big.QuotaBytes)
	require.Equal(t, int64(13), big.SeatLimit)

	unlimitedFree := Config{FreeSeats: 1, SeatIncludedBytes: 1, StoragePackBytes: 1}
	require.Zero(t, unlimitedFree.Entitlement(&Subscription{Status: StatusActive, Seats: 2}).QuotaBytes,
		"an unlimited free tier stays unlimited when paid")

	require.Equal(t, int64(math.MaxInt64), cfg.Entitlement(&Subscription{Status: StatusActive, Seats: math.MaxInt64}).SeatLimit)
}

func TestEntitlement_Overflow(t *testing.T) {
	cfg := Config{FreeSeats: 1, FreeQuotaBytes: 1, SeatIncludedBytes: math.MaxInt64 / 2, StoragePackBytes: math.MaxInt64}

	got := cfg.Entitlement(&Subscription{Status: StatusActive, Seats: 3, StoragePacks: 1})
	require.Equal(t, int64(math.MaxInt64), got.QuotaBytes)

	got = cfg.Entitlement(&Subscription{Status: StatusActive, Seats: 1, StoragePacks: 1})
	require.Equal(t, int64(math.MaxInt64), got.QuotaBytes, "the sum saturates too")

	require.Equal(t, int64(6), mulSaturating(2, 3))
	require.Zero(t, mulSaturating(0, math.MaxInt64))
	require.Equal(t, int64(math.MaxInt64), addSaturating(math.MaxInt64, 1))
}
