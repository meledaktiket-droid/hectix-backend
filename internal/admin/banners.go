package admin

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type banner struct {
	ID           string      `json:"id"`
	Tag          string      `json:"tag"`
	Description  string      `json:"description"`
	ImageURL     string      `json:"imageUrl"`
	DisplayOrder int         `json:"displayOrder"`
	IsActive     bool        `json:"isActive"`
	DeletedAt    *httpx.Time `json:"deletedAt"`
	CreatedAt    httpx.Time  `json:"createdAt"`
	UpdatedAt    httpx.Time  `json:"updatedAt"`
}

const bannerCols = `id, tag, description, image_url, display_order, is_active, deleted_at, created_at, updated_at`

type bannerBody struct {
	Tag          any   `json:"tag"`
	Description  any   `json:"description"`
	ImageURL     any   `json:"imageUrl"`
	DisplayOrder *int  `json:"displayOrder"`
	IsActive     *bool `json:"isActive"`
}

func (b bannerBody) fields() (tag, desc, img string, ok bool) {
	tag, ok1 := b.Tag.(string)
	desc, ok2 := b.Description.(string)
	img, ok3 := b.ImageURL.(string)
	return tag, desc, img, ok1 && ok2 && ok3 && tag != "" && desc != "" && img != ""
}

func (h *handlers) listBanners(w http.ResponseWriter, r *http.Request) {
	rows, err := list[banner](r.Context(), h.d.DB, `select `+bannerCols+` from banner
		where deleted_at is null order by display_order, created_at`)
	if err != nil {
		serverError(w, "banners", err)
		return
	}
	httpx.JSON(w, http.StatusOK, rows)
}

func (h *handlers) createBanner(w http.ResponseWriter, r *http.Request) {
	var b bannerBody
	if err := httpx.Decode(r, &b); err != nil {
		badBody(w)
		return
	}
	tag, desc, img, ok := b.fields()
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "Missing required fields")
		return
	}
	if !isAssetURL(img) {
		httpx.Error(w, http.StatusBadRequest, "Invalid image URL")
		return
	}
	order, active := 0, true
	if b.DisplayOrder != nil {
		order = *b.DisplayOrder
	}
	if b.IsActive != nil {
		active = *b.IsActive
	}
	now := time.Now().UTC()
	row, err := one[banner](r.Context(), h.d.DB, `insert into banner(id, tag, description, image_url, display_order, is_active, created_at, updated_at)
		values($1, $2, $3, $4, $5, $6, $7, $7) returning `+bannerCols, dbx.UUID(), tag, desc, img, order, active, now)
	if err != nil {
		serverError(w, "create banner", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, row)
}

func (h *handlers) updateBanner(w http.ResponseWriter, r *http.Request) {
	var b bannerBody
	if err := httpx.Decode(r, &b); err != nil {
		badBody(w)
		return
	}
	tag, desc, img, ok := b.fields()
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "Missing required fields")
		return
	}
	if !isAssetURL(img) {
		httpx.Error(w, http.StatusBadRequest, "Invalid image URL")
		return
	}
	row, err := one[banner](r.Context(), h.d.DB, `update banner set tag = $2, description = $3, image_url = $4,
		display_order = coalesce($5, display_order), is_active = coalesce($6, is_active), updated_at = $7
		where id = $1 and deleted_at is null returning `+bannerCols,
		chi.URLParam(r, "id"), tag, desc, img, b.DisplayOrder, b.IsActive, time.Now().UTC())
	if err != nil {
		serverError(w, "update banner", err)
		return
	}
	if row == nil {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	httpx.JSON(w, http.StatusOK, row)
}

func (h *handlers) deleteBanner(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	tag, err := h.d.DB.Exec(r.Context(), `update banner set deleted_at = $2, updated_at = $2
		where id = $1 and deleted_at is null`, chi.URLParam(r, "id"), now)
	if err != nil {
		serverError(w, "delete banner", err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	ok(w)
}
