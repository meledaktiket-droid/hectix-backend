package organizer

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"hectix/backend/internal/dbx"
)

var validStatuses = []string{"paid", "pending_payment", "cancelled", "expired"}

type orderParams struct {
	Status   string
	EventID  string
	From, To string
	Q        string
	Page     int64
	PageSize int64
}

const maxPage = 100000

var ymd = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func isValidDate(v string) bool {
	if !ymd.MatchString(v) {
		return false
	}
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

func parseOrderParams(q url.Values) orderParams {
	var p orderParams
	if s := q.Get("status"); slices.Contains(validStatuses, s) {
		p.Status = s
	}
	if v := q.Get("from"); isValidDate(v) {
		p.From = v
	}
	if v := q.Get("to"); isValidDate(v) {
		p.To = v
	}
	p.Q = jsTrim(q.Get("q"))
	p.EventID = jsTrim(q.Get("eventId"))

	p.Page = 1
	if n, ok := jsParseInt(firstOr(q, "page", "1")); ok && n != 0 {
		p.Page = min(maxPage, max(1, n))
	}
	size := int64(25)
	if n, ok := jsParseInt(firstOr(q, "pageSize", "25")); ok && n != 0 {
		size = n
	}
	p.PageSize = min(100, max(1, size))
	return p
}

func firstOr(q url.Values, k, def string) string {
	if v, ok := q[k]; ok && len(v) > 0 {
		return v[0]
	}
	return def
}

func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
}

var wib = time.FixedZone("WIB", 7*3600)

func wibStart(date string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", date, wib)
	return t.UTC()
}

func wibEndExclusive(date string) time.Time { return wibStart(date).AddDate(0, 0, 1) }

type orderWhere struct {
	base      []string
	args      []any
	statusArg string
}

func buildOrderConditions(p orderParams, eventIDs []string) *orderWhere {
	w := &orderWhere{statusArg: p.Status}
	w.add("o.event_id = any($%d)", eventIDs)
	if p.EventID != "" && slices.Contains(eventIDs, p.EventID) {
		w.add("o.event_id = $%d", p.EventID)
	}
	if p.From != "" {
		w.add("o.created_at >= $%d", wibStart(p.From))
	}
	if p.To != "" {
		w.add("o.created_at < $%d", wibEndExclusive(p.To))
	}
	if p.Q != "" {
		w.args = append(w.args, "%"+p.Q+"%")
		n := len(w.args)
		w.base = append(w.base, fmt.Sprintf("(u.name ilike $%d or u.email ilike $%d)", n, n))
	}
	return w
}

func (w *orderWhere) add(cond string, arg any) {
	w.args = append(w.args, arg)
	w.base = append(w.base, fmt.Sprintf(cond, len(w.args)))
}

func (w *orderWhere) sql(withStatus bool) (string, []any) {
	conds, args := slices.Clone(w.base), slices.Clone(w.args)
	if withStatus && w.statusArg != "" {
		args = append(args, w.statusArg)
		conds = append(conds, fmt.Sprintf("o.status = $%d", len(args)))
	}
	return strings.Join(conds, " and "), args
}

type orderItemSummary struct {
	OrderID        string `json:"orderId"`
	TicketTypeName string `json:"ticketTypeName"`
	Quantity       int64  `json:"quantity"`
}

func itemsByOrder(ctx context.Context, db dbx.DB, orderIDs []string) (map[string][]orderItemSummary, error) {
	out := map[string][]orderItemSummary{}
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, `select order_id, ticket_type_name, quantity from order_item where order_id = any($1)`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it orderItemSummary
		if err := rows.Scan(&it.OrderID, &it.TicketTypeName, &it.Quantity); err != nil {
			return nil, err
		}
		out[it.OrderID] = append(out[it.OrderID], it)
	}
	return out, rows.Err()
}

func itemsOf(m map[string][]orderItemSummary, id string) []orderItemSummary {
	if v := m[id]; v != nil {
		return v
	}
	return []orderItemSummary{}
}
