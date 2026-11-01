package admin

import "testing"

func TestBanners(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/banners", nil)
	e.guarded(t, "POST", "/api/banners", map[string]any{"tag": "a", "description": "b", "imageUrl": "/uploads/banners/c.jpg"})
	e.guarded(t, "PUT", "/api/banners/x", map[string]any{"tag": "a", "description": "b", "imageUrl": "/uploads/banners/c.jpg"})
	e.guarded(t, "DELETE", "/api/banners/x", nil)

	want(t, e.do(t, "GET", "/api/banners", nil, e.staff), 200, `[]`)

	for _, body := range []any{
		map[string]any{"tag": "a", "description": "b"},
		map[string]any{"tag": "", "description": "b", "imageUrl": "/uploads/banners/c.jpg"},
		map[string]any{"tag": 5, "description": "b", "imageUrl": "/uploads/banners/c.jpg"},
	} {
		want(t, e.do(t, "POST", "/api/banners", body, e.staff), 400, `{"error":"Missing required fields"}`)
	}
	want(t, e.do(t, "POST", "/api/banners", "{", e.staff), 400, `{"error":"Invalid request body"}`)
	for _, img := range []string{"javascript:alert(1)", "http://x/i.jpg", "//evil.com/i.jpg", "data:image/png;base64,AA", "i.jpg",
		"/api/files/verification/u/k.png", "/uploads/../etc", "https:///i.jpg", "https://u:p@x/i.jpg", "https://x/a b.jpg"} {
		want(t, e.do(t, "POST", "/api/banners", map[string]any{"tag": "a", "description": "b", "imageUrl": img}, e.staff), 400, `{"error":"Invalid image URL"}`)
	}

	rec := e.do(t, "POST", "/api/banners", map[string]any{"tag": "Second", "description": "<p>d</p>", "imageUrl": "https://x/i.jpg", "displayOrder": 2}, e.staff)
	want(t, rec, 201, "")
	second := decode[banner](t, rec)
	if second.Tag != "Second" || second.DisplayOrder != 2 || !second.IsActive || second.DeletedAt != nil || len(second.ID) != 36 {
		t.Fatalf("created %+v", second)
	}
	rec = e.do(t, "POST", "/api/banners", map[string]any{"tag": "First", "description": "d", "imageUrl": "/uploads/banners/i.jpg", "isActive": false}, e.admin)
	want(t, rec, 201, "")
	first := decode[banner](t, rec)
	if first.DisplayOrder != 0 || first.IsActive {
		t.Fatalf("defaults %+v", first)
	}

	rec = e.do(t, "GET", "/api/banners", nil, e.staff)
	assertGolden(t, "banners.json", rec)
	got := decode[[]banner](t, rec)
	if len(got) != 2 || got[0].Tag != "First" || got[1].Tag != "Second" {
		t.Fatalf("order %+v", got)
	}

	rec = e.do(t, "PUT", "/api/banners/"+second.ID, map[string]any{"tag": "Second!", "description": "d2", "imageUrl": "/uploads/banners/i2.jpg"}, e.staff)
	want(t, rec, 200, "")
	upd := decode[banner](t, rec)
	if upd.Tag != "Second!" || upd.DisplayOrder != 2 || !upd.IsActive || !upd.UpdatedAt.After(second.UpdatedAt.Time) {
		t.Fatalf("updated %+v", upd)
	}
	rec = e.do(t, "PUT", "/api/banners/"+second.ID, map[string]any{"tag": "S", "description": "d", "imageUrl": "/uploads/banners/i.jpg", "displayOrder": 0, "isActive": false}, e.staff)
	if upd = decode[banner](t, rec); upd.DisplayOrder != 0 || upd.IsActive {
		t.Fatalf("updated %+v", upd)
	}
	want(t, e.do(t, "PUT", "/api/banners/nope", map[string]any{"tag": "a", "description": "b", "imageUrl": "/uploads/banners/c.jpg"}, e.staff), 404, `{"error":"Not found"}`)
	want(t, e.do(t, "PUT", "/api/banners/"+second.ID, map[string]any{"tag": "a"}, e.staff), 400, `{"error":"Missing required fields"}`)
	want(t, e.do(t, "PUT", "/api/banners/"+second.ID, map[string]any{"tag": "a", "description": "b", "imageUrl": "javascript:x"}, e.staff), 400, `{"error":"Invalid image URL"}`)

	want(t, e.do(t, "DELETE", "/api/banners/"+second.ID, nil, e.staff), 200, `{"ok":true}`)
	want(t, e.do(t, "DELETE", "/api/banners/"+second.ID, nil, e.staff), 404, `{"error":"Not found"}`)
	want(t, e.do(t, "PUT", "/api/banners/"+second.ID, map[string]any{"tag": "a", "description": "b", "imageUrl": "/uploads/banners/c.jpg"}, e.staff), 404, `{"error":"Not found"}`)
	var n int
	e.scalar(t, &n, `select count(*) from banner where deleted_at is not null`)
	if n != 1 {
		t.Fatalf("soft-deleted %d", n)
	}
	if got := decode[[]banner](t, e.do(t, "GET", "/api/banners", nil, e.staff)); len(got) != 1 {
		t.Fatalf("after delete %+v", got)
	}
}
