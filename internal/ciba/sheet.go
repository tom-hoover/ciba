package ciba

import (
	"image"

	"github.com/tom-hoover/darkroom/sheet"
)

// ContactSheet renders img in every supplied look at thumbnail size and tiles
// the results into one labelled image. Each label is exactly the string to
// pass to -style.
//
// The layout lives in darkroom/sheet so both pipelines share one
// implementation of the cell geometry and label fitting.
func ContactSheet(img image.Image, looks []Look, px int) image.Image {
	names := make([]string, len(looks))
	for i, l := range looks {
		names[i] = l.Name
	}
	return sheet.Build(img, names, px, func(thumb image.Image, i int) image.Image {
		return Render(thumb, looks[i])
	})
}
