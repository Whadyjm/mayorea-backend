package domain

import "time"

// Cents is a monetary amount in hundredths of a currency unit (USD or Bs).
// Money is never a float: every amount is an integer number of cents.
type Cents int64

// Rate is the BCV exchange rate in ten-thousandths (36,5214 Bs/$ -> 365214),
// so the four decimals published by the BCV are preserved.
type Rate int64

const RateScale = 10_000

// BsFromUsd converts USD cents to Bs cents at the given rate.
func BsFromUsd(usd Cents, rate Rate) Cents {
	return Cents(roundDiv(int64(usd)*int64(rate), RateScale))
}

// UsdFromBs converts Bs cents to USD cents at the given rate.
func UsdFromBs(bs Cents, rate Rate) Cents {
	if rate <= 0 {
		return 0
	}
	return Cents(roundDiv(int64(bs)*RateScale, int64(rate)))
}

// roundDiv divides rounding half away from zero.
func roundDiv(n, d int64) int64 {
	if n < 0 {
		return -((-n + d/2) / d)
	}
	return (n + d/2) / d
}

// Dates are calendar days stored as UTC midnight.

func DateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func DaysBetween(from, to time.Time) int {
	return int(DateOf(to).Sub(DateOf(from)) / (24 * time.Hour))
}

func AddDays(t time.Time, days int) time.Time {
	return DateOf(t).AddDate(0, 0, days)
}
