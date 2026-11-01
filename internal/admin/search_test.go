package admin

import (
	"net/url"
	"testing"
)

func TestSearchEscapesWildcards(t *testing.T) {
	e := setup(t)
	e.seedOrg(t, "o1", "Org", "active")
	e.seedEvent(t, "e1", "o1", "Promo 50% Off", "published")
	e.seedEvent(t, "e2", "o1", "Gig 500", "published")
	e.seedEvent(t, "e3", "o1", `Back\slash a_b`, "published")
	e.seedTicketType(t, "t1", "e1", 1000, 10, 0)
	e.Exec(t, `update "user" set name = '50% Club' where id = 'usr'`)
	e.Exec(t, `update "user" set name = 'a_b' where id = 'stf'`)
	e.seedOrder(t, "a", "usr", "e1", "t1", "paid", "qris", 1, 1000, "1 hour")
	e.seedOrder(t, "b", "adm", "e1", "t1", "paid", "qris", 1, 1000, "1 hour")

	get := func(path, q string) int64 {
		t.Helper()
		rec := e.do(t, "GET", path+"?q="+url.QueryEscape(q), nil, e.staff)
		want(t, rec, 200, "")
		return int64(decode[struct{ Total int64 }](t, rec).Total)
	}
	for _, c := range []struct {
		path, q string
		want    int64
	}{
		{"/api/events", "50%", 1},
		{"/api/events", "%", 1},
		{"/api/events", "_", 1},
		{"/api/events", `\s`, 1},
		{"/api/events", "a_b", 1},
		{"/api/events", "axb", 0},
		{"/api/users", "50%", 1},
		{"/api/users", "%", 1},
		{"/api/users", "_", 1},
		{"/api/orders", "50%", 1},
		{"/api/orders", "%", 1},
		{"/api/orders", "_", 0},
	} {
		if got := get(c.path, c.q); got != c.want {
			t.Errorf("%s q=%q: total %d want %d", c.path, c.q, got, c.want)
		}
	}
}
