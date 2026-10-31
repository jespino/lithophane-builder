package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"

	"litofany-builder/internal/lithophane"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("litofany-builder", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "source image (JPEG, PNG, or GIF; first frame)")
	output := flags.String("output", "lithophane.stl", "destination binary STL file")
	options := lithophane.Options{}
	flags.Float64Var(&options.Width, "width", 100, "panel width in millimeters (height preserves aspect ratio)")
	flags.Float64Var(&options.Step, "step", 0.5, "maximum mesh spacing in millimeters (smaller adds detail)")
	flags.Float64Var(&options.MinThickness, "min-thickness", 0.8, "thickness of white pixels in millimeters")
	flags.Float64Var(&options.MaxThickness, "max-thickness", 3, "thickness of black pixels in millimeters")
	flags.BoolVar(&options.Invert, "invert", false, "make light pixels thicker instead of dark pixels")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *input == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: litofany-builder -input photo.jpg [-output lithophane.stl]; use -help for options")
	}
	if *output == "" {
		return fmt.Errorf("output path must not be empty")
	}
	source, err := os.Open(*input)
	if err != nil {
		return fmt.Errorf("open image: %w", err)
	}
	img, _, decodeErr := image.Decode(source)
	closeErr := source.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode image: %w", decodeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	mesh, err := lithophane.New(img, options)
	if err != nil {
		return err
	}
	// Write beside the destination, then rename so failures leave no partial STL.
	dest, err := os.CreateTemp(filepath.Dir(*output), ".lithophane-*.stl")
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer os.Remove(dest.Name())
	if err := mesh.WriteSTL(dest); err != nil {
		dest.Close()
		return fmt.Errorf("write STL: %w", err)
	}
	if err := dest.Close(); err != nil {
		return fmt.Errorf("close STL: %w", err)
	}
	if err := os.Rename(dest.Name(), *output); err != nil {
		return fmt.Errorf("save STL: %w", err)
	}
	fmt.Fprintf(stdout, "Created %s: %.2f × %.2f mm, %.2f–%.2f mm thick, %d triangles\n",
		*output, mesh.Width, mesh.Height, options.MinThickness, options.MaxThickness, mesh.TriangleCount())
	return nil
}
