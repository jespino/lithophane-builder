package lithophane

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

func TestThicknessAndOrientation(t *testing.T) {
	img := image.NewNRGBA(image.Rect(5, 7, 7, 9))
	img.SetNRGBA(5, 7, color.NRGBA{A: 255}) // Top left: black.
	img.SetNRGBA(6, 7, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	img.SetNRGBA(5, 8, color.NRGBA{R: 255, A: 255})
	// Bottom right is transparent and should be treated as white.
	for _, invert := range []bool{false, true} {
		m, err := New(img, Options{Width: 10, Step: 10, MinThickness: 1, MaxThickness: 3, Invert: invert})
		if err != nil {
			t.Fatal(err)
		}
		black, white := float32(3), float32(1)
		if invert {
			black, white = white, black
		}
		if m.point(0, 1, true)[2] != black || m.point(1, 1, true)[2] != white || m.point(1, 0, true)[2] != white {
			t.Fatalf("unexpected thickness or orientation: %v", m.heights)
		}
	}
}

func TestSTLClosedAndOutward(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 3, 2))
	img.SetGray(1, 0, color.Gray{Y: 128})
	img.SetGray(2, 1, color.Gray{Y: 255})
	m, err := New(img, Options{Width: 9, Step: 2, MinThickness: 0.8, MaxThickness: 3})
	if err != nil {
		t.Fatal(err)
	}
	if m.Height != 6 {
		t.Fatalf("height = %v", m.Height)
	}
	var buf bytes.Buffer
	if err := m.WriteSTL(&buf); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	count := binary.LittleEndian.Uint32(data[80:84])
	if count != m.TriangleCount() || len(data) != 84+int(count)*50 {
		t.Fatal("incorrect STL size/count")
	}
	type edge struct{ a, b vertex }
	edges := map[edge]int{}
	volume := float64(0)
	for offset := 84; offset < len(data); offset += 50 {
		var values [12]float64
		for i := range values {
			values[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset+4*i:])))
		}
		a, b, c := vertex{}, vertex{}, vertex{}
		for i := 0; i < 3; i++ {
			a[i] = float32(values[3+i])
			b[i] = float32(values[6+i])
			c[i] = float32(values[9+i])
		}
		for _, e := range []edge{{a, b}, {b, c}, {c, a}} {
			edges[e]++
		}
		u, v := [3]float64{}, [3]float64{}
		for i := 0; i < 3; i++ {
			u[i] = float64(b[i]) - float64(a[i])
			v[i] = float64(c[i]) - float64(a[i])
		}
		cross := [3]float64{u[1]*v[2] - u[2]*v[1], u[2]*v[0] - u[0]*v[2], u[0]*v[1] - u[1]*v[0]}
		dot, length := float64(0), float64(0)
		for i := 0; i < 3; i++ {
			dot += cross[i] * values[i]
			length += values[i] * values[i]
		}
		if dot <= 0 || math.Abs(length-1) > 1e-6 {
			t.Fatal("degenerate triangle or invalid normal")
		}
		volume += (float64(a[0])*(float64(b[1])*float64(c[2])-float64(b[2])*float64(c[1])) + float64(a[1])*(float64(b[2])*float64(c[0])-float64(b[0])*float64(c[2])) + float64(a[2])*(float64(b[0])*float64(c[1])-float64(b[1])*float64(c[0]))) / 6
	}
	for e, n := range edges {
		if n != 1 || edges[edge{e.b, e.a}] != 1 {
			t.Fatalf("edge is not shared exactly twice with opposite winding: %v", e)
		}
	}
	if volume < 9*6*0.8 || volume > 9*6*3 {
		t.Fatalf("invalid signed volume %v", volume)
	}
}

func TestValidation(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	for _, o := range []Options{
		{Width: 0, Step: 1, MinThickness: 1, MaxThickness: 3},
		{Width: 10, Step: math.NaN(), MinThickness: 1, MaxThickness: 3},
		{Width: 10, Step: 1, MinThickness: 3, MaxThickness: 1},
		{Width: 10, Step: 1e-20, MinThickness: 1, MaxThickness: 3},
		{Width: math.Inf(1), Step: 1, MinThickness: 1, MaxThickness: 3},
	} {
		if _, err := New(img, o); err == nil {
			t.Errorf("accepted invalid options %+v", o)
		}
	}
	if _, err := New(nil, Options{Width: 10, Step: 1, MinThickness: 1, MaxThickness: 3}); err == nil {
		t.Fatal("accepted nil image")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriterFailure(t *testing.T) {
	m, err := New(image.NewGray(image.Rect(0, 0, 1, 1)), Options{Width: 10, Step: 10, MinThickness: 1, MaxThickness: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WriteSTL(failedWriter{}); err == nil {
		t.Fatal("lost writer error")
	}
}
