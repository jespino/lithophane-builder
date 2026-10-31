// Package lithophane converts images into closed, flat-backed relief meshes.
package lithophane

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

type Options struct {
	Width, Step, MinThickness, MaxThickness float64
	Invert                                  bool
}

type Mesh struct {
	Width, Height float64
	columns, rows int
	heights       []float32
}

// New composites transparency onto white, converts to grayscale, and samples a
// regular grid. Image top maps to positive Y, preserving its front-view orientation.
func New(img image.Image, o Options) (*Mesh, error) {
	for _, v := range []float64{o.Width, o.Step, o.MinThickness, o.MaxThickness} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return nil, fmt.Errorf("width, step, and thicknesses must be finite positive numbers")
		}
	}
	if o.MaxThickness < o.MinThickness {
		return nil, fmt.Errorf("max-thickness must be at least min-thickness")
	}
	if img == nil || img.Bounds().Empty() {
		return nil, fmt.Errorf("image must not be empty")
	}
	b := img.Bounds()
	height := o.Width * float64(b.Dy()) / float64(b.Dx())
	nx, ny := math.Max(1, math.Ceil(o.Width/o.Step)), math.Max(1, math.Ceil(height/o.Step))
	// Bound memory, runtime, and binary STL's uint32 triangle count before allocation.
	if math.IsInf(height, 0) || nx > 1e6 || ny > 1e6 || (nx+1)*(ny+1) > 1e6 {
		return nil, fmt.Errorf("mesh exceeds 1,000,000 vertices per surface; increase -step or reduce -width")
	}
	// STL uses float32 coordinates. Reject dimensions that collapse at that precision.
	for _, v := range []float64{o.Width, height, o.MinThickness, o.MaxThickness} {
		if float32(v) == 0 || math.IsInf(float64(float32(v)), 0) {
			return nil, fmt.Errorf("dimensions must be representable as positive float32 values")
		}
	}
	m := &Mesh{Width: o.Width, Height: height, columns: int(nx) + 1, rows: int(ny) + 1}
	gray := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			gray.SetGray(x, y, color.GrayModel.Convert(color.RGBA64{
				R: uint16(r + 65535 - a), G: uint16(g + 65535 - a), B: uint16(bl + 65535 - a), A: 65535,
			}).(color.Gray))
		}
	}
	m.heights = make([]float32, m.columns*m.rows)
	for y := 0; y < m.rows; y++ {
		sy := float64(m.rows-1-y) / ny * float64(b.Dy()-1)
		for x := 0; x < m.columns; x++ {
			sx := float64(x) / nx * float64(b.Dx()-1)
			brightness := sample(gray, sx, sy) / 255
			if o.Invert {
				brightness = 1 - brightness
			}
			m.heights[y*m.columns+x] = float32(o.MinThickness + (1-brightness)*(o.MaxThickness-o.MinThickness))
		}
	}
	return m, nil
}

func sample(img *image.Gray, x, y float64) float64 {
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, img.Rect.Max.X-1), min(y0+1, img.Rect.Max.Y-1)
	fx, fy := x-float64(x0), y-float64(y0)
	top := float64(img.GrayAt(x0, y0).Y)*(1-fx) + float64(img.GrayAt(x1, y0).Y)*fx
	bottom := float64(img.GrayAt(x0, y1).Y)*(1-fx) + float64(img.GrayAt(x1, y1).Y)*fx
	return top*(1-fy) + bottom*fy
}

func (m *Mesh) TriangleCount() uint32 {
	return uint32(4*(m.columns-1)*(m.rows-1) + 4*(m.columns+m.rows-2))
}

type vertex [3]float32

func (m *Mesh) point(x, y int, front bool) vertex {
	z := float32(0)
	if front {
		z = m.heights[y*m.columns+x]
	}
	return vertex{float32(float64(x) / float64(m.columns-1) * m.Width), float32(float64(y) / float64(m.rows-1) * m.Height), z}
}

// WriteSTL streams little-endian binary STL with outward-facing unit normals.
// Both surfaces share the side walls' boundary vertices, forming a closed solid.
func (m *Mesh) WriteSTL(w io.Writer) error {
	out := bufio.NewWriter(w)
	var header [84]byte
	copy(header[:80], "Lithophane; coordinates in millimeters")
	binary.LittleEndian.PutUint32(header[80:], m.TriangleCount())
	if _, err := out.Write(header[:]); err != nil {
		return err
	}
	var writeErr error
	triangle := func(a, b, c vertex) {
		if writeErr != nil {
			return
		}
		u, v := [3]float64{}, [3]float64{}
		for i := range u {
			u[i] = float64(b[i]) - float64(a[i])
			v[i] = float64(c[i]) - float64(a[i])
		}
		n := [3]float64{u[1]*v[2] - u[2]*v[1], u[2]*v[0] - u[0]*v[2], u[0]*v[1] - u[1]*v[0]}
		length := math.Sqrt(n[0]*n[0] + n[1]*n[1] + n[2]*n[2])
		if length == 0 {
			writeErr = fmt.Errorf("dimensions collapse at STL precision; use larger dimensions or increase -step")
			return
		}
		var record [50]byte
		values := [12]float32{float32(n[0] / length), float32(n[1] / length), float32(n[2] / length), a[0], a[1], a[2], b[0], b[1], b[2], c[0], c[1], c[2]}
		for i, value := range values {
			binary.LittleEndian.PutUint32(record[i*4:], math.Float32bits(value))
		}
		_, writeErr = out.Write(record[:])
	}
	for y := 0; y < m.rows-1; y++ {
		for x := 0; x < m.columns-1; x++ {
			for _, front := range []bool{true, false} {
				a, b, c, d := m.point(x, y, front), m.point(x+1, y, front), m.point(x+1, y+1, front), m.point(x, y+1, front)
				if front {
					triangle(a, b, c)
					triangle(a, c, d)
				} else {
					triangle(a, c, b)
					triangle(a, d, c)
				}
			}
		}
	}
	wall := func(x0, y0, x1, y1 int) {
		a, b := m.point(x0, y0, false), m.point(x1, y1, false)
		c, d := m.point(x1, y1, true), m.point(x0, y0, true)
		triangle(a, b, c)
		triangle(a, c, d)
	}
	for x := 0; x < m.columns-1; x++ {
		wall(x, 0, x+1, 0)
		wall(x+1, m.rows-1, x, m.rows-1)
	}
	for y := 0; y < m.rows-1; y++ {
		wall(m.columns-1, y, m.columns-1, y+1)
		wall(0, y+1, 0, y)
	}
	if writeErr != nil {
		return writeErr
	}
	return out.Flush()
}
