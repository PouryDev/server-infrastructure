package web

import (
	"fmt"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/google/uuid"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
)

func (s *Server) mediaUpload(w http.ResponseWriter, r *http.Request, u shop.User) {
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	default:
		jsonOut(w, 429, map[string]string{"error": "کمی بعد دوباره تلاش کنید"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		failure(w, shop.ErrInvalid)
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, h, e := r.FormFile("file")
	if e != nil {
		failure(w, shop.ErrInvalid)
		return
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 8<<20))
	if e != nil || len(b) == 0 || len(b) >= 8<<20 {
		failure(w, shop.ErrInvalid)
		return
	}
	kind := http.DetectContentType(b)
	if !slices.Contains([]string{"image/jpeg", "image/png", "image/webp"}, kind) {
		failure(w, fmt.Errorf("%w: تصویر PNG، JPEG یا WebP انتخاب کنید", shop.ErrInvalid))
		return
	}
	id := uuid.NewString()
	path := filepath.Join(s.Uploads, "media-"+id)
	if e = os.WriteFile(path, b, 0644); e != nil {
		failure(w, fmt.Errorf("ذخیره تصویر انجام نشد"))
		return
	}
	a := shop.Upload{ID: id, UserID: u.ID, Name: filepath.Base(h.Filename), MIME: kind, Size: int64(len(b)), CreatedAt: time.Now()}
	if e = s.App.SaveMedia(r.Context(), u, a); e != nil {
		_ = os.Remove(path)
		failure(w, e)
		return
	}
	jsonOut(w, 201, map[string]string{"url": "/media/" + id})
}
func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	a, e := s.App.Media(r.Context(), r.PathValue("id"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.MIME)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, filepath.Join(s.Uploads, "media-"+a.ID))
}
func (s *Server) download(w http.ResponseWriter, r *http.Request, u shop.User) {
	a, e := s.App.File(r.Context(), u, r.PathValue("id"))
	if e != nil {
		failure(w, e)
		return
	}
	if a.UserID != u.ID && !u.CanManage() {
		failure(w, shop.ErrForbidden)
		return
	}
	w.Header().Set("Content-Type", a.MIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	http.ServeFile(w, r, filepath.Join(s.Uploads, a.ID))
}
