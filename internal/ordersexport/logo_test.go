package ordersexport

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 2
	chunk := func(typ string, data []byte) {
		binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(typ)
		b.Write(data)
		binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	}
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	return b.Bytes()
}

func dataURL(b []byte) *string {
	s := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
	return &s
}

func TestDecodeLogoRejectsHugeDimensions(t *testing.T) {
	for _, d := range [][2]uint32{{20000, 20000}, {10001, 1}, {1, 10001}, {8000, 8000}} {
		raw := pngHeader(d[0], d[1])
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width != int(d[0]) {
			t.Fatalf("crafted header not decodable: %v %v", cfg, err)
		}
		if decodeLogo(dataURL(raw)) != nil {
			t.Errorf("%v accepted", d)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3)))
	if l := decodeLogo(dataURL(buf.Bytes())); l == nil || l.w != 4 || l.h != 3 {
		t.Fatalf("small logo rejected: %v", l)
	}
	s := dataURL(pngHeader(50000, 50000))
	if _, err := PDF(nil, Meta{OrgName: "Org", LogoDataURL: s}); err != nil {
		t.Fatal(err)
	}
}
