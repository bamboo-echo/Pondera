package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestAppIconPNGUsesPonderaDarkRoundedBackground(t *testing.T) {
	f, err := os.Open("build/appicon.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}

	assertRoundedPlateIcon(t, img, 1024)
}

func TestWindowsICOUsesPonderaDarkRoundedBackground(t *testing.T) {
	for _, size := range []int{16, 24, 32, 48, 64, 256} {
		t.Run(fmt.Sprintf("%dx%d", size, size), func(t *testing.T) {
			img := decodeICOImage(t, "build/windows/icon.ico", size)
			assertRoundedPlateIcon(t, img, size)
		})
	}
}

func assertRoundedPlateIcon(t *testing.T, img image.Image, size int) {
	t.Helper()

	bounds := img.Bounds()
	if bounds.Dx() != size || bounds.Dy() != size {
		t.Fatalf("app icon must be square, got %dx%d", bounds.Dx(), bounds.Dy())
	}

	corners := []struct {
		name string
		x    int
		y    int
	}{
		{"top-left", bounds.Min.X, bounds.Min.Y},
		{"top-right", bounds.Max.X - 1, bounds.Min.Y},
		{"bottom-left", bounds.Min.X, bounds.Max.Y - 1},
		{"bottom-right", bounds.Max.X - 1, bounds.Max.Y - 1},
	}
	for _, corner := range corners {
		_, _, _, a := img.At(corner.x, corner.y).RGBA()
		if a > 0xff {
			t.Fatalf("%s corner must be transparent, alpha=%d", corner.name, a)
		}
	}

	_, _, _, centerAlpha := img.At(bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2).RGBA()
	if centerAlpha == 0 {
		t.Fatal("app icon center must contain visible artwork")
	}

	// The plate is inset from the canvas edge; the artwork itself stays inside
	// a rounded dark tile. Sample just inside the tile on all four sides.
	inset := size / 16
	if inset < 1 {
		inset = 1
	}
	platePoints := []struct {
		name string
		x    int
		y    int
	}{
		{"top", bounds.Min.X + bounds.Dx()/2, bounds.Min.Y + inset},
		{"right", bounds.Max.X - 1 - inset, bounds.Min.Y + bounds.Dy()/2},
		{"bottom", bounds.Min.X + bounds.Dx()/2, bounds.Max.Y - 1 - inset},
		{"left", bounds.Min.X + inset, bounds.Min.Y + bounds.Dy()/2},
	}
	for _, point := range platePoints {
		_, _, _, a := img.At(point.x, point.y).RGBA()
		if a == 0 {
			t.Fatalf("%s edge must contain the rounded plate background", point.name)
		}
		assertPonderaPlateColor(t, point.name, img.At(point.x, point.y))
	}

	// At 32px and above the plate must not bleed onto the canvas edge; the
	// 16/24px frames keep a faint antialiased edge pixel there.
	if size >= 32 {
		_, _, _, edgeAlpha := img.At(bounds.Min.X+bounds.Dx()/2, bounds.Min.Y).RGBA()
		if edgeAlpha != 0 {
			t.Fatalf("top edge must stay transparent outside the inset plate, alpha=%d", edgeAlpha)
		}
	}
}

func assertPonderaPlateColor(t *testing.T, name string, colorValue color.Color) {
	t.Helper()

	r16, g16, b16, _ := colorValue.RGBA()
	r, g, b := uint8(r16>>8), uint8(g16>>8), uint8(b16>>8)
	// #0B1120 plate with a subtle vertical gradient; allow for that spread.
	if !near(r, 0x0b, 14) || !near(g, 0x11, 14) || !near(b, 0x20, 14) {
		t.Fatalf("%s edge must use the Pondera dark plate, got #%02x%02x%02x", name, r, g, b)
	}
}

func near(got, want uint8, tolerance uint8) bool {
	if got > want {
		return got-want <= tolerance
	}
	return want-got <= tolerance
}

func decodeICOImage(t *testing.T, path string, size int) image.Image {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)

	var header struct {
		Reserved uint16
		Type     uint16
		Count    uint16
	}
	if err := binary.Read(r, binary.LittleEndian, &header); err != nil {
		t.Fatal(err)
	}
	if header.Reserved != 0 || header.Type != 1 {
		t.Fatalf("invalid ICO header: reserved=%d type=%d", header.Reserved, header.Type)
	}

	type iconEntry struct {
		Width       uint8
		Height      uint8
		ColorCount  uint8
		Reserved    uint8
		Planes      uint16
		BitCount    uint16
		BytesInRes  uint32
		ImageOffset uint32
	}

	entries := make([]iconEntry, header.Count)
	for i := range entries {
		if err := binary.Read(r, binary.LittleEndian, &entries[i]); err != nil {
			t.Fatal(err)
		}
	}

	expectedSizes := map[int]bool{16: false, 24: false, 32: false, 48: false, 64: false, 256: false}
	targetIndex := -1
	for i, entry := range entries {
		width := int(entry.Width)
		height := int(entry.Height)
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		if width != height {
			t.Fatalf("ICO image must be square, got %dx%d", width, height)
		}
		if _, ok := expectedSizes[width]; ok {
			expectedSizes[width] = true
		}
		if width == size {
			targetIndex = i
		}
	}
	for expectedSize, found := range expectedSizes {
		if !found {
			t.Fatalf("ICO is missing %dx%d image", expectedSize, expectedSize)
		}
	}
	if targetIndex < 0 {
		t.Fatalf("ICO is missing %dx%d image", size, size)
	}

	entry := entries[targetIndex]
	end := int(entry.ImageOffset + entry.BytesInRes)
	if end > len(data) {
		t.Fatalf("ICO image offset exceeds file size: offset=%d size=%d file=%d", entry.ImageOffset, entry.BytesInRes, len(data))
	}
	payload := data[entry.ImageOffset:end]
	if bytes.HasPrefix(payload, []byte("\x89PNG\r\n\x1a\n")) {
		img, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	return decodeDIBImage(t, payload, size)
}

// decodeDIBImage decodes the uncompressed 32-bit bottom-up BGRA frame that
// Windows icons use below 128px. The header height is doubled because it
// counts the XOR color bitmap plus the AND mask.
func decodeDIBImage(t *testing.T, data []byte, size int) image.Image {
	t.Helper()

	if len(data) < 40 {
		t.Fatalf("DIB frame truncated: %d bytes", len(data))
	}
	headerSize := int(binary.LittleEndian.Uint32(data[0:4]))
	if headerSize < 40 || headerSize > len(data) {
		t.Fatalf("unsupported DIB header size %d", headerSize)
	}
	width := int(int32(binary.LittleEndian.Uint32(data[4:8])))
	height := int(int32(binary.LittleEndian.Uint32(data[8:12])))
	bitCount := int(binary.LittleEndian.Uint16(data[14:16]))
	compression := binary.LittleEndian.Uint32(data[16:20])
	if width != size || height != size*2 || bitCount != 32 || compression != 0 {
		t.Fatalf("unexpected DIB frame: %dx%d bitCount=%d compression=%d", width, height, bitCount, compression)
	}
	pixels := data[headerSize:]
	if len(pixels) < size*size*4 {
		t.Fatalf("DIB pixel data truncated: %d bytes for %dx%d", len(pixels), size, size)
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		row := pixels[(size-1-y)*size*4 : (size-y)*size*4]
		for x := 0; x < size; x++ {
			offset := x * 4
			img.SetNRGBA(x, y, color.NRGBA{R: row[offset+2], G: row[offset+1], B: row[offset], A: row[offset+3]})
		}
	}
	return img
}
