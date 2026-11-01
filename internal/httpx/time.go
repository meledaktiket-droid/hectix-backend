package httpx

import (
	"database/sql/driver"
	"fmt"
	"time"
)

type Time struct{ time.Time }

const jsISO = "2006-01-02T15:04:05.000Z"

func (t Time) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.UTC().Truncate(time.Millisecond).Format(jsISO) + `"`), nil
}

func (t *Time) UnmarshalJSON(b []byte) error {
	return t.Time.UnmarshalJSON(b)
}

func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		t.Time = v
		return nil
	case nil:
		t.Time = time.Time{}
		return nil
	}
	return fmt.Errorf("httpx.Time: cannot scan %T", src)
}

func (t Time) Value() (driver.Value, error) { return t.Time, nil }
