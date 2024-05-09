package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func timeFromDate(t *testing.T, date string) time.Time {
	d, err := time.Parse(time.DateOnly, date)
	assert.NoError(t, err)
	return d
}

func Test_PaymentsComingUp(t *testing.T) {
	day := 24 * time.Hour

	now := time.Now()
	future := now.Add(10 * day)
	past := now.Add(-10 * day)

	payments := []*Payment{
		NewPayment("foo").WithDueDate(now),
		NewPayment("bar1").WithDueDate(future),
		NewPayment("bar2").WithDueDate(future),
		NewPayment("bar3").WithDueDate(future.Add(day)),
		NewPayment("baz").WithDueDate(past),
		NewPayment("baz2").WithDueDate(past),
		NewPayment("null"),
	}

	msg := SummarizePaymentsComingUp(payments)
	assert.Contains(t, msg, future.Format("2006-01-02"))
	assert.Contains(t, msg, "bar1")
	assert.Contains(t, msg, "bar2")
	assert.NotContains(t, msg, "bar3")

	assert.NotContains(t, msg, "foo")
	assert.NotContains(t, msg, "baz")
	assert.NotContains(t, msg, "nul")
}

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
		{
			"2023-11-03T09:36:22+02:00",
			"2023-11-12T23:11:22+02:00",
			9,
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

func Test_FindPaymentsUntil(t *testing.T) {
	today := timeFromDate(t, "2023-11-05")
	payments := []*Payment{
		NewPayment("foo").WithDueDate(timeFromDate(t, "2023-11-04")),
		NewPayment("bar").WithDueDate(timeFromDate(t, "2023-11-05")),
		NewPayment("baz").WithDueDate(timeFromDate(t, "2023-11-06")),
	}
	delayed := FindPaymentsUntil(payments, 0, today)
	require.Equal(t, 2, len(delayed))
	assert.Equal(t, "foo", delayed[0].description)
	assert.Equal(t, "bar", delayed[1].description)
}

func Test_FindPaymentsAt(t *testing.T) {
	today := timeFromDate(t, "2023-11-05")
	payments := []*Payment{
		NewPayment("foo").WithDueDate(timeFromDate(t, "2023-11-04")),
		NewPayment("bar").WithDueDate(timeFromDate(t, "2023-11-05")),
		NewPayment("baz").WithDueDate(timeFromDate(t, "2023-11-06")),
	}
	delayed := FindPaymentsAt(payments, 0, today)
	require.Equal(t, 1, len(delayed))
	assert.Equal(t, "bar", delayed[0].description)
}
