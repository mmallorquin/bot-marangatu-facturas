package telegram

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // para decodificar PNG además de JPEG
)

// Calidad del JPEG de la foto enderezada: suficiente para leer números chicos sin pesar de más.
const rotatedJPEGQuality = 90

// rotateImage gira la imagen en sentido horario (90, 180 o 270 grados) y la devuelve como JPEG.
// Un formato que no se puede decodificar (por ejemplo WebP) devuelve error y el que llama manda la original.
func rotateImage(data []byte, degrees int) ([]byte, error) {
	degrees = ((degrees % 360) + 360) % 360
	if degrees%90 != 0 || degrees == 0 {
		return nil, fmt.Errorf("giro inválido: %d grados", degrees)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decodificando la imagen: %w", err)
	}
	bounds := decoded.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), decoded, bounds.Min, draw.Src)

	var dst *image.RGBA
	if degrees == 180 {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var tx, ty int
			switch degrees {
			case 90:
				tx, ty = h-1-y, x
			case 180:
				tx, ty = w-1-x, h-1-y
			case 270:
				tx, ty = y, w-1-x
			}
			from := src.PixOffset(x, y)
			to := dst.PixOffset(tx, ty)
			copy(dst.Pix[to:to+4], src.Pix[from:from+4])
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: rotatedJPEGQuality}); err != nil {
		return nil, fmt.Errorf("codificando la imagen girada: %w", err)
	}
	return out.Bytes(), nil
}
