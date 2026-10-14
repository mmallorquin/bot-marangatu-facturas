package telegram

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// tinyPNG es una imagen de 16x8: la mitad izquierda roja, la derecha azul.
// Son bloques JPEG enteros, así el color no se mezcla al comprimir.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			c := color.RGBA{R: 255, A: 255}
			if x >= 8 {
				c = color.RGBA{B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r > 0xC000 && g < 0x4000 && b < 0x4000
}

func TestRotateImageTurnsClockwise(t *testing.T) {
	cases := []struct {
		degrees       int
		width, height int
		redAt         image.Point
	}{
		{90, 8, 16, image.Pt(4, 4)},   // el rojo (izquierda) pasa arriba
		{180, 16, 8, image.Pt(12, 4)}, // el rojo pasa a la derecha
		{270, 8, 16, image.Pt(4, 12)}, // el rojo pasa abajo
	}
	for _, tc := range cases {
		out, err := rotateImage(tinyPNG(t), tc.degrees)
		if err != nil {
			t.Fatalf("%d°: %v", tc.degrees, err)
		}
		img, format, err := image.Decode(bytes.NewReader(out))
		if err != nil || format != "jpeg" {
			t.Fatalf("%d°: la salida es %s, %v", tc.degrees, format, err)
		}
		if b := img.Bounds(); b.Dx() != tc.width || b.Dy() != tc.height {
			t.Errorf("%d°: tamaño %dx%d", tc.degrees, b.Dx(), b.Dy())
		}
		if !isRed(img.At(tc.redAt.X, tc.redAt.Y)) {
			t.Errorf("%d°: el rojo no quedó en %v", tc.degrees, tc.redAt)
		}
	}
}

func TestRotateImageRejectsUselessOrUndecodableInput(t *testing.T) {
	if _, err := rotateImage(tinyPNG(t), 0); err == nil {
		t.Error("0° no es un giro")
	}
	if _, err := rotateImage(tinyPNG(t), 45); err == nil {
		t.Error("45° no se soporta")
	}
	if _, err := rotateImage([]byte("no es una imagen"), 90); err == nil {
		t.Error("bytes que no son imagen")
	}
}
