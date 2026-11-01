package organizer

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hectix/backend/internal/ordersexport"
)

var exportStatusLabels = map[string]string{
	"paid": "Paid", "pending_payment": "Pending", "cancelled": "Cancelled", "expired": "Expired",
}

var monthsEN = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func fmtDay(d string) string {
	t, _ := time.Parse("2006-01-02", d)
	return fmt.Sprintf("%d %s %d", t.Day(), monthsEN[t.Month()-1], t.Year())
}

func buildFilterLabel(p orderParams, eventName string) string {
	var parts []string
	if p.Status != "" {
		l, ok := exportStatusLabels[p.Status]
		if !ok {
			l = p.Status
		}
		parts = append(parts, l)
	}
	if eventName != "" {
		parts = append(parts, eventName)
	}
	switch {
	case p.From != "" && p.To != "":
		parts = append(parts, fmtDay(p.From)+" – "+fmtDay(p.To))
	case p.From != "":
		parts = append(parts, "From "+fmtDay(p.From))
	case p.To != "":
		parts = append(parts, "Until "+fmtDay(p.To))
	}
	if p.Q != "" {
		parts = append(parts, `"`+p.Q+`"`)
	}
	if len(parts) == 0 {
		return "All orders"
	}
	return strings.Join(parts, " · ")
}

func (h *handlers) exportOrders(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	format := "xlsx"
	if q.Get("format") == "pdf" {
		format = "pdf"
	}
	orgName, logo := "Organization", (*string)(nil)
	var name *string
	if err := h.db.QueryRow(ctx, `select name, logo from organization where id = $1`, c.orgID).Scan(&name, &logo); err == nil && name != nil {
		orgName = *name
	}
	ids, names, err := h.orgEvents(r, c.orgID)
	if err != nil {
		serverError(w, "export events", err)
		return
	}
	p := parseOrderParams(q)
	rows := []ordersexport.Row{}
	var revenue int64
	if len(ids) > 0 {
		where, args := buildOrderConditions(p, ids).sql(true)
		dbRows, err := h.db.Query(ctx, `select o.id, o.status, o.total, o.subtotal, o.voucher_code, o.voucher_discount,
			o.payment_method, o.bank, o.created_at, o.event_id, coalesce(o.reference_number, o.id), u.name, u.email
			from "order" o join "user" u on u.id = o.user_id where `+where+` order by o.created_at desc`, args...)
		if err != nil {
			serverError(w, "export orders", err)
			return
		}
		var orderIDs []string
		for dbRows.Next() {
			var row ordersexport.Row
			var id, method, eventID string
			var bank *string
			if err := dbRows.Scan(&id, &row.Status, &row.Total, &row.Subtotal, &row.VoucherCode, &row.VoucherDiscount,
				&method, &bank, &row.CreatedAt, &eventID, &row.ReferenceNumber, &row.BuyerName, &row.BuyerEmail); err != nil {
				dbRows.Close()
				serverError(w, "export orders", err)
				return
			}
			row.EventName = "Unknown event"
			if n, ok := names[eventID]; ok {
				row.EventName = n
			}
			row.PaymentMethod = ordersexport.PaymentLabel(method, bank)
			row.CreatedAt = row.CreatedAt.UTC()
			rows = append(rows, row)
			orderIDs = append(orderIDs, id)
		}
		dbRows.Close()
		if err := dbRows.Err(); err != nil {
			serverError(w, "export orders", err)
			return
		}
		items, err := itemsByOrder(ctx, h.db, orderIDs)
		if err != nil {
			serverError(w, "export items", err)
			return
		}
		for i := range rows {
			var its []ordersexport.Item
			for _, it := range items[orderIDs[i]] {
				its = append(its, ordersexport.Item{Quantity: int(it.Quantity), TicketTypeName: it.TicketTypeName})
			}
			rows[i].Tickets = ordersexport.TicketSummary(its)
			if rows[i].Status == "paid" {
				revenue += rows[i].Subtotal
			}
		}
	}
	eventName := ""
	if p.EventID != "" {
		eventName = names[p.EventID]
	}
	now := time.Now().UTC()
	meta := ordersexport.Meta{
		OrgName:     orgName,
		FilterLabel: buildFilterLabel(p, eventName),
		GeneratedAt: now,
		Revenue:     revenue,
		OrderCount:  len(rows),
	}
	var (
		body []byte
		ct   string
	)
	if format == "pdf" {
		meta.LogoDataURL = h.logoDataURL(ctx, logo)
		body, err = ordersexport.PDF(rows, meta)
		ct = ordersexport.ContentTypePDF
	} else {
		body, err = ordersexport.XLSX(rows, meta)
		ct = ordersexport.ContentTypeXLSX
	}
	if err != nil {
		serverError(w, "export render", err)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", ordersexport.ContentDisposition(ordersexport.Filename(format, now)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

const maxLogoBytes = 5 << 20

func (h *handlers) logoDataURL(ctx context.Context, logo *string) *string {
	if logo == nil || *logo == "" {
		return nil
	}
	u, err := url.Parse(*logo)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	var data []byte
	ct := ""
	if key, ok := h.localUpload(u); ok {
		if h.uploadDir == "" {
			return nil
		}
		f, err := os.Open(filepath.Join(h.uploadDir, "public", filepath.FromSlash(key)))
		if err != nil {
			return nil
		}
		defer f.Close()
		if data, err = io.ReadAll(io.LimitReader(f, maxLogoBytes+1)); err != nil || len(data) > maxLogoBytes {
			return nil
		}
		ct = http.DetectContentType(data)
	} else {
		data, ct = h.fetchLogo(ctx, u.String())
		if data == nil {
			return nil
		}
	}
	if ct == "" {
		ct = "image/png"
	}
	s := "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)
	return &s
}

func (h *handlers) localUpload(u *url.URL) (string, bool) {
	for host := range h.apps {
		if !strings.EqualFold(u.Host, host) || !strings.HasPrefix(u.Path, "/uploads/") {
			continue
		}
		key := strings.TrimPrefix(path.Clean(u.Path), "/uploads/")
		if key == "" || strings.HasPrefix(key, ".") || strings.Contains(key, "..") {
			return "", false
		}
		return key, true
	}
	return "", false
}

func (h *handlers) fetchLogo(ctx context.Context, rawURL string) ([]byte, string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ""
	}
	client := h.logoClient
	if client == nil {
		client = publicOnlyClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, ""
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, ""
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxLogoBytes+1))
	if err != nil || len(data) > maxLogoBytes {
		return nil, ""
	}
	return data, res.Header.Get("Content-Type")
}

var errPrivateAddr = errors.New("logo fetch: non-public address")

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",
		"100.64.0.0/10",
		"192.0.0.0/24",
		"198.18.0.0/15",
		"240.0.0.0/4",
		"64:ff9b::/96",
		"64:ff9b:1::/48",
		"2002::/16",
		"fc00::/7",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

func publicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var publicOnlyClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 3 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if !publicIP(net.ParseIP(host)) {
					return errPrivateAddr
				}
				return nil
			},
		}).DialContext,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return http.ErrUseLastResponse
		}
		return nil
	},
}
