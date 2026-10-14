package main

import "time"

// Business months use Shanghai's UTC+8 civil dates independently of the host
// and database session timezone. Database activity timestamps are stored in UTC.
var businessLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func businessDateRangeUTC(from, to string) (string, string, error) {
	start, err := time.ParseInLocation("2006-01-02", from, businessLocation)
	if err != nil {
		return "", "", fail(400, "请选择有效月份")
	}
	end, err := time.ParseInLocation("2006-01-02", to, businessLocation)
	if err != nil {
		return "", "", fail(400, "请选择有效月份")
	}
	const layout = "2006-01-02 15:04:05"
	return start.UTC().Format(layout), end.UTC().Format(layout), nil
}

func businessMonthRangeUTC(month string) (string, string, error) {
	from, to, err := monthRange(month)
	if err != nil {
		return "", "", err
	}
	return businessDateRangeUTC(from, to)
}
