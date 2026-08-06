package service

import (
	"expvar"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorklistObservabilityTracksBalancedConnectionsAndOutcomes(t *testing.T) {
	activeBefore := worklistSSEConnectionsActive.Value()
	publishedBefore := expvarMapIntValue(worklistNotificationsTotal, "published")

	ObserveWorklistSSEConnectionOpened()
	require.Equal(t, activeBefore+1, worklistSSEConnectionsActive.Value())
	ObserveWorklistNotification("published")
	ObserveWorklistSSEConnectionClosed()

	require.Equal(t, activeBefore, worklistSSEConnectionsActive.Value())
	require.Equal(t, publishedBefore+1, expvarMapIntValue(worklistNotificationsTotal, "published"))
}

func expvarMapIntValue(metric *expvar.Map, key string) int64 {
	value := metric.Get(key)
	if value == nil {
		return 0
	}
	counter, ok := value.(*expvar.Int)
	if !ok {
		return 0
	}
	return counter.Value()
}
