// SPDX-License-Identifier: ice License 1.0

package social

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebScrapperInvalidConfig(t *testing.T) {
	t.Parallel()

	sc := newMustWebScraper(string([]byte{0x00}), "")
	require.NotNil(t, sc)

	impl, ok := sc.(*webScraperImpl)
	require.True(t, ok)
	require.NotNil(t, impl)

	require.Panics(t, func() {
		impl.BuildQuery("foo", nil)
	})

	t.Run("EmptyURL", func(t *testing.T) {
		require.Panics(t, func() {
			_ = newMustWebScraper("", "")
		})
	})
}

func TestDataFetcherHead(t *testing.T) {
	t.Parallel()

	fetcher := &dataFetcherImpl{Censorer: new(censorerImpl)}

	t.Run("OK", func(t *testing.T) {
		location, err := fetcher.Head(context.TODO(), "https://httpstat.us/301")
		require.NoError(t, err)
		require.Equal(t, "https://httpstat.us", location)
	})
	t.Run("ServerError", func(t *testing.T) {
		_, err := fetcher.Head(context.TODO(), "https://httpstat.us/500")
		t.Logf("fetcher error: %v", err)
		require.Error(t, err)
	})
}
