package ticketpdf

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxBannerBytes  = 15 << 20
	maxBannerSide   = 10000
	maxBannerPixels = 40e6
)

type bannerImage struct {
	data []byte
	typ  string
	w, h float64
}

type BannerLoader func(url string) ([]byte, error)

var errNotLocal = errors.New("ticketpdf: banner is not a local upload")

func LocalBanners(uploadDir string, hosts []string) BannerLoader {
	allowed := map[string]bool{}
	for _, h := range hosts {
		allowed[strings.ToLower(h)] = true
	}
	return func(raw string) ([]byte, error) {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !allowed[strings.ToLower(u.Host)] {
			return nil, errNotLocal
		}
		key, ok := strings.CutPrefix(u.Path, "/uploads/")
		if !ok || key == "" || path.Clean(key) != key || strings.HasPrefix(key, ".") || strings.Contains(key, `\`) {
			return nil, errNotLocal
		}
		root, err := os.OpenRoot(filepath.Join(uploadDir, "public"))
		if err != nil {
			return nil, err
		}
		defer root.Close()
		f, err := root.Open(key)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() || st.Size() > maxBannerBytes {
			return nil, errNotLocal
		}
		b, err := io.ReadAll(io.LimitReader(f, maxBannerBytes+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxBannerBytes {
			return nil, errNotLocal
		}
		return b, nil
	}
}

func loadBanner(load BannerLoader, url string) *bannerImage {
	if load == nil || url == "" {
		return nil
	}
	raw, err := load(url)
	if err != nil || len(raw) > maxBannerBytes {
		return nil
	}
	return normaliseBanner(raw)
}

func normaliseBanner(raw []byte) *bannerImage {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil
	}
	if cfg.Width > maxBannerSide || cfg.Height > maxBannerSide || float64(cfg.Width)*float64(cfg.Height) > maxBannerPixels {
		return nil
	}
	b := &bannerImage{w: float64(cfg.Width), h: float64(cfg.Height)}
	if format == "jpeg" && (cfg.ColorModel == color.YCbCrModel || cfg.ColorModel == color.GrayModel) {
		b.data, b.typ = raw, "JPG"
		return b
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	var out bytes.Buffer
	if format == "jpeg" {
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
		if jpeg.Encode(&out, rgba, &jpeg.Options{Quality: 90}) != nil {
			return nil
		}
		b.data, b.typ = out.Bytes(), "JPG"
		return b
	}
	nrgba := image.NewNRGBA(img.Bounds())
	draw.Draw(nrgba, nrgba.Bounds(), img, img.Bounds().Min, draw.Src)
	if png.Encode(&out, nrgba) != nil {
		return nil
	}
	b.data, b.typ = out.Bytes(), "PNG"
	return b
}
