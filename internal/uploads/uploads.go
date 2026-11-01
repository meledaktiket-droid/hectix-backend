package uploads

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/httpx"
)

const (
	maxSize  = 10 * 1024 * 1024
	tokenTTL = 300 * time.Second
	private  = "verification/"
	tmpDir   = ".tmp"

	maxFolderLen   = 100
	maxFolderDepth = 4

	rateLimit  = 60
	rateWindow = time.Hour
)

type Uploads struct {
	Dir     string
	Secret  string
	DB      *pgxpool.Pool
	IsStaff func(role string) bool

	now func() time.Time

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

func (u *Uploads) allow(userID string) bool {
	now := u.clock()
	cutoff := now.Add(-rateWindow)
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.hits == nil {
		u.hits = map[string][]time.Time{}
	}
	if now.Sub(u.lastSweep) > rateWindow {
		for id, ts := range u.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(u.hits, id)
			}
		}
		u.lastSweep = now
	}
	ts := u.hits[userID]
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	ts = ts[i:]
	if len(ts) >= rateLimit {
		u.hits[userID] = ts
		return false
	}
	u.hits[userID] = append(ts, now)
	return true
}

func validKey(key string) bool {
	return key != "" && path.Clean(key) == key && !strings.HasPrefix(key, "/") &&
		!strings.HasPrefix(key, ".") && !strings.Contains(key, `\`)
}

var exts = map[string][]string{
	"image/jpeg":      {"jpg", "jpeg"},
	"image/png":       {"png"},
	"image/webp":      {"webp"},
	"application/pdf": {"pdf"},
}

var images = []string{"image/jpeg", "image/png", "image/webp"}

var policies = map[string]struct {
	types  []string
	prefix func(userID string) string
}{
	"web":       {images, func(id string) string { return "profile/" + id }},
	"organizer": {append(images[:3:3], "application/pdf"), func(id string) string { return "verification/" + id }},
	"admin":     {images, func(string) string { return "banners" }},
}

var folderRe = regexp.MustCompile(`^[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*$`)

var b64 = base64.RawURLEncoding

type claims struct {
	Key         string `json:"key"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Exp         int64  `json:"exp"`
}

func (u *Uploads) clock() time.Time {
	if u.now != nil {
		return u.now()
	}
	return time.Now()
}

func (u *Uploads) mac(payload string) []byte {
	k := hmac.New(sha256.New, []byte(u.Secret))
	k.Write([]byte("hectix uploads"))
	m := hmac.New(sha256.New, k.Sum(nil))
	m.Write([]byte(payload))
	return m.Sum(nil)
}

func (u *Uploads) sign(c claims) string {
	b, _ := json.Marshal(c)
	p := b64.EncodeToString(b)
	return p + "." + b64.EncodeToString(u.mac(p))
}

func (u *Uploads) verify(tok string) (claims, bool) {
	var c claims
	p, s, ok := strings.Cut(tok, ".")
	if !ok {
		return c, false
	}
	sig, err := b64.DecodeString(s)
	if err != nil || !hmac.Equal(sig, u.mac(p)) {
		return c, false
	}
	raw, err := b64.DecodeString(p)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, false
	}
	if u.clock().Unix() > c.Exp {
		return c, false
	}
	return c, true
}

func uuid4() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func ext(filename, contentType string) string {
	allowed := exts[contentType]
	if i := strings.LastIndex(filename, "."); i >= 0 {
		e := strings.ToLower(filename[i+1:])
		for _, a := range allowed {
			if e == a {
				return e
			}
		}
	}
	return allowed[0]
}

func visibility(key string) string {
	if strings.HasPrefix(key, private) {
		return "private"
	}
	return "public"
}

func (u *Uploads) Upload(w http.ResponseWriter, r *http.Request) {
	u.upload(w, r, auth.UserFrom(r.Context()))
}

func (u *Uploads) upload(w http.ResponseWriter, r *http.Request, user *auth.User) {
	app := httpx.AppFrom(r.Context())
	pol, ok := policies[app.Name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !u.allow(user.ID) {
		httpx.Error(w, http.StatusTooManyRequests, "Too many uploads")
		return
	}
	var in struct {
		Filename    string  `json:"filename"`
		ContentType string  `json:"contentType"`
		Size        float64 `json:"size"`
		Folder      *string `json:"folder"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	allowed := false
	for _, t := range pol.types {
		allowed = allowed || t == in.ContentType
	}
	if !allowed {
		httpx.Error(w, http.StatusBadRequest, "File type not allowed")
		return
	}
	if in.Size > maxSize {
		httpx.Error(w, http.StatusBadRequest, "File too large (max 10MB)")
		return
	}
	if in.Size <= 0 || in.Size != math.Trunc(in.Size) {
		httpx.Error(w, http.StatusBadRequest, "Invalid file size")
		return
	}
	prefix := pol.prefix(user.ID)
	if in.Folder != nil {
		prefix = *in.Folder
		if !folderRe.MatchString(prefix) || len(prefix) > maxFolderLen ||
			strings.Count(prefix, "/")+1 > maxFolderDepth ||
			(strings.HasPrefix(prefix+"/", private) && prefix != private+user.ID) {
			httpx.Error(w, http.StatusBadRequest, "Invalid folder")
			return
		}
	}
	key := prefix + "/" + uuid4() + "." + ext(in.Filename, in.ContentType)
	tok := u.sign(claims{Key: key, ContentType: in.ContentType, Size: int64(in.Size), Exp: u.clock().Add(tokenTTL).Unix()})

	publicURL := app.BaseURL + "/uploads/" + key
	if visibility(key) == "private" {
		publicURL = app.BaseURL + "/api/files/" + key
	}
	httpx.JSON(w, http.StatusOK, map[string]string{
		"url": "/api/upload/put?t=" + tok, "key": key, "publicUrl": publicURL,
	})
}

func (u *Uploads) Put(w http.ResponseWriter, r *http.Request) {
	c, ok := u.verify(r.URL.Query().Get("t"))
	if !ok || !validKey(c.Key) {
		httpx.Error(w, http.StatusForbidden, "Invalid or expired upload token")
		return
	}
	if r.Header.Get("Content-Type") != c.ContentType {
		httpx.Error(w, http.StatusBadRequest, "Content-Type does not match")
		return
	}
	if r.ContentLength > c.Size {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "File too large")
		return
	}
	body := http.MaxBytesReader(w, r.Body, c.Size)
	head := make([]byte, 512)
	n, err := io.ReadFull(body, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		status, msg := readFailure(err)
		httpx.Error(w, status, msg)
		return
	}
	head = head[:n]
	if http.DetectContentType(head) != c.ContentType {
		httpx.Error(w, http.StatusBadRequest, "File content does not match its type")
		return
	}

	target := visibility(c.Key) + "/" + c.Key
	if err := os.MkdirAll(filepath.Join(u.Dir, tmpDir), 0o700); err != nil {
		serverError(w, err)
		return
	}
	root, err := os.OpenRoot(u.Dir)
	if err != nil {
		serverError(w, err)
		return
	}
	defer root.Close()
	if _, err := root.Lstat(target); err == nil {
		httpx.Error(w, http.StatusConflict, "File already exists")
		return
	}
	tmp := tmpDir + "/" + uuid4()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		serverError(w, err)
		return
	}
	defer root.Remove(tmp)
	err = writeAll(f, head, body, c.Size)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	var ue uploadError
	switch {
	case errors.As(err, &ue):
		httpx.Error(w, ue.status, ue.msg)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	if err := root.MkdirAll(path.Dir(target), 0o755); err != nil {
		serverError(w, err)
		return
	}
	if err := root.Link(tmp, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			httpx.Error(w, http.StatusConflict, "File already exists")
			return
		}
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type uploadError struct {
	status int
	msg    string
}

func (e uploadError) Error() string { return e.msg }

func writeAll(f *os.File, head []byte, body io.Reader, size int64) error {
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if _, err := f.Write(head); err != nil {
		return err
	}
	m, err := io.Copy(f, body)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return err
		}
		status, msg := readFailure(err)
		return uploadError{status, msg}
	}
	if int64(len(head))+m != size {
		return uploadError{http.StatusBadRequest, "File size does not match"}
	}
	return nil
}

func (u *Uploads) CleanStaleTemp() {
	if u.Dir == "" {
		return
	}
	dir := filepath.Join(u.Dir, tmpDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := u.clock().Add(-time.Hour)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && fi.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				log.Printf("uploads: clean temp: %v", err)
			}
		}
	}
}

func readFailure(err error) (int, string) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return http.StatusRequestEntityTooLarge, "File too large"
	}
	return http.StatusBadRequest, "Failed to read upload"
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("uploads: %v", err)
	httpx.Error(w, http.StatusInternalServerError, "Internal server error")
}

var fileTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".webp": "image/webp", ".pdf": "application/pdf",
}

func (u *Uploads) File(w http.ResponseWriter, r *http.Request) {
	u.file(w, r, auth.UserFrom(r.Context()), chi.URLParam(r, "*"))
}

func (u *Uploads) file(w http.ResponseWriter, r *http.Request, user *auth.User, key string) {
	segs := strings.Split(key, "/")
	if !strings.HasPrefix(key, private) || path.Clean(key) != key || len(segs) < 3 || strings.Contains(key, `\`) {
		http.NotFound(w, r)
		return
	}
	ok := segs[1] == user.ID ||
		(httpx.AppFrom(r.Context()).Name == "admin" && u.IsStaff != nil && u.IsStaff(user.Role))
	if !ok {
		err := u.DB.QueryRow(r.Context(), `select exists(
			select 1 from organization o
			join member viewer on viewer.organization_id = o.id and viewer.user_id = $1
			join member uploader on uploader.organization_id = o.id and uploader.user_id = $2
			where strpos(o.verification_data, $3) > 0)`, user.ID, segs[1], key).Scan(&ok)
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(filepath.Join(u.Dir, "private"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	ct := fileTypes[strings.ToLower(path.Ext(key))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
