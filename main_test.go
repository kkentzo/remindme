package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_Payment_DiffFromToday(t *testing.T) {
	kases := []struct {
		now  string
		due  string
		diff int
	}{
		{
			"2023-11-03T09:36:22+02:00",
			"2023-11-03T12:43:22+02:00",
			0,
		},
		{
			"2023-11-03T09:36:22+02:00",
			"2023-11-04T23:11:22+02:00",
			1,
		},
		{
			"2023-11-03T09:36:22+02:00",
			"2023-11-02T23:11:22+02:00",
			-1,
		},
	}

	for _, kase := range kases {
		now, err := time.Parse(time.RFC3339, kase.now)
		assert.NoError(t, err)
		due, err := time.Parse(time.RFC3339, kase.due)
		assert.NoError(t, err)

		p := NewPayment("foo").WithDueDate(due)
		assert.Equal(t, kase.diff, p.DiffFromNowInDays(now))
	}

}
